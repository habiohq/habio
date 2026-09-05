// Package homeassistant provides a small REST proof-of-concept Provider. It is
// intentionally generic over Home Assistant entity IDs and service actions;
// device models remain outside Habio core.
package homeassistant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/habiohq/habio"
)

const (
	ProviderID      = "homeassistant"
	maxResponseSize = 1 << 20
)

var (
	ErrInvalidConfig    = errors.New("homeassistant: invalid config")
	ErrTargetNotFound   = errors.New("homeassistant: logical target not found")
	ErrInvalidTarget    = errors.New("homeassistant: invalid resolved target")
	ErrInvalidAction    = errors.New("homeassistant: invalid action")
	ErrUnexpectedStatus = errors.New("homeassistant: unexpected HTTP status")
	ErrResponseTooLarge = errors.New("homeassistant: response too large")
)

// Config supplies a local Home Assistant URL, long-lived access token, logical
// target bindings, and optional HTTP/time dependencies.
type Config struct {
	BaseURL  string
	Token    string
	Bindings map[string]string
	Client   *http.Client
	Now      func() time.Time
}

// Provider implements Habio Provider, Resolver, and Observer through Home
// Assistant's REST API.
type Provider struct {
	baseURL  *url.URL
	token    string
	bindings map[string]string
	client   *http.Client
	now      func() time.Time
}

// New constructs a Home Assistant REST proof-of-concept provider.
func New(config Config) (*Provider, error) {
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("%w: base URL must be absolute HTTP(S)", ErrInvalidConfig)
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("%w: base URL cannot contain query or fragment", ErrInvalidConfig)
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, fmt.Errorf("%w: token is required", ErrInvalidConfig)
	}
	bindings := make(map[string]string, len(config.Bindings))
	for logical, entityID := range config.Bindings {
		if strings.TrimSpace(logical) == "" || logical != strings.TrimSpace(logical) {
			return nil, fmt.Errorf("%w: invalid logical target %q", ErrInvalidConfig, logical)
		}
		if _, _, err := splitEntityID(entityID); err != nil {
			return nil, fmt.Errorf("%w: binding %q: %v", ErrInvalidConfig, logical, err)
		}
		bindings[logical] = entityID
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	return &Provider{baseURL: baseURL, token: config.Token, bindings: bindings, client: client, now: now}, nil
}

// Resolve maps an Action's logical target to one configured Home Assistant
// entity ID. No discovery or device registry is implemented.
func (p *Provider) Resolve(_ context.Context, action habio.Action) (habio.ResolvedTarget, error) {
	entityID, ok := p.bindings[action.Target()]
	if !ok {
		return habio.ResolvedTarget{}, fmt.Errorf("%w: %s", ErrTargetNotFound, action.Target())
	}
	return habio.NewResolvedTarget(ProviderID, []byte(entityID))
}

// Dispatch calls POST /api/services/<domain>/<service>. A successful HTTP
// response is an acknowledgement, not proof of physical state.
func (p *Provider) Dispatch(ctx context.Context, attempt habio.ExecutionAttempt, action habio.Action, target habio.ResolvedTarget) (habio.DispatchResult, error) {
	if target.Provider() != ProviderID {
		return p.result(attempt.ID(), habio.DispatchNotDispatched, nil, fmt.Errorf("%w: provider %q", ErrInvalidTarget, target.Provider()))
	}
	entityID := string(target.Endpoint())
	domain, _, err := splitEntityID(entityID)
	if err != nil {
		return p.result(attempt.ID(), habio.DispatchNotDispatched, nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err))
	}
	if !validSegment(action.Name()) {
		return p.result(attempt.ID(), habio.DispatchNotDispatched, nil, fmt.Errorf("%w: invalid service action %q", ErrInvalidAction, action.Name()))
	}
	body, err := serviceData(action.Input(), entityID)
	if err != nil {
		return p.result(attempt.ID(), habio.DispatchNotDispatched, nil, err)
	}

	requestURL := p.apiURL("services", domain, action.Name())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return p.result(attempt.ID(), habio.DispatchNotDispatched, nil, err)
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		// Once handed to the HTTP client, Habio cannot generally prove whether
		// Home Assistant received or executed the service action.
		return p.result(attempt.ID(), habio.DispatchUnknown, nil, err)
	}
	defer resp.Body.Close()
	evidence, err := readLimited(resp.Body)
	if err != nil {
		return p.result(attempt.ID(), habio.DispatchDispatched, nil, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return p.result(attempt.ID(), habio.DispatchDispatched, nil, fmt.Errorf("%w: %s", ErrUnexpectedStatus, resp.Status))
	}
	receipt, err := habio.NewReceipt(habio.ReceiptSpec{
		Provider: ProviderID, AttemptID: attempt.ID(), ReceivedAt: p.now(), Evidence: evidence,
	})
	if err != nil {
		return habio.DispatchResult{}, err
	}
	return p.result(attempt.ID(), habio.DispatchAcknowledged, &receipt, nil)
}

// Observe fetches GET /api/states/<entity_id> and preserves Home Assistant's
// last_updated as ObservedAt.
func (p *Provider) Observe(ctx context.Context, action habio.Action, target habio.ResolvedTarget) ([]habio.Observation, error) {
	if target.Provider() != ProviderID {
		return nil, fmt.Errorf("%w: provider %q", ErrInvalidTarget, target.Provider())
	}
	entityID := string(target.Endpoint())
	if _, _, err := splitEntityID(entityID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL("states", entityID), nil)
	if err != nil {
		return nil, err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: %s", ErrUnexpectedStatus, resp.Status)
	}
	var state struct {
		EntityID    string    `json:"entity_id"`
		LastUpdated time.Time `json:"last_updated"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("homeassistant: decode state: %w", err)
	}
	if state.EntityID != entityID {
		return nil, fmt.Errorf("homeassistant: state entity %q does not match %q", state.EntityID, entityID)
	}
	if state.LastUpdated.IsZero() {
		return nil, errors.New("homeassistant: state has no last_updated time")
	}
	hash := sha256.Sum256(append([]byte(entityID+"\x00"), body...))
	observation, err := habio.NewObservation(habio.ObservationSpec{
		ID:         habio.ObservationID("ha-" + hex.EncodeToString(hash[:])),
		Source:     ProviderID + "/rest",
		Target:     action.Target(),
		Value:      body,
		ObservedAt: state.LastUpdated,
		RecordedAt: p.now(),
	})
	if err != nil {
		return nil, err
	}
	return []habio.Observation{observation}, nil
}

func (p *Provider) result(attemptID habio.AttemptID, status habio.DispatchStatus, receipt *habio.Receipt, operationErr error) (habio.DispatchResult, error) {
	result, err := habio.NewDispatchResult(habio.DispatchResultSpec{
		Provider: ProviderID, AttemptID: attemptID, Status: status, Receipt: receipt,
	})
	if err != nil {
		return habio.DispatchResult{}, err
	}
	return result, operationErr
}

func (p *Provider) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
}

func (p *Provider) apiURL(parts ...string) string {
	u := *p.baseURL
	escaped := make([]string, len(parts))
	for i, part := range parts {
		escaped[i] = url.PathEscape(part)
	}
	u.Path += "/api/" + strings.Join(escaped, "/")
	return u.String()
}

func splitEntityID(entityID string) (string, string, error) {
	domain, objectID, ok := strings.Cut(entityID, ".")
	if !ok || !validSegment(domain) || !validSegment(objectID) {
		return "", "", fmt.Errorf("invalid entity ID %q", entityID)
	}
	return domain, objectID, nil
}

func validSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func serviceData(input []byte, entityID string) ([]byte, error) {
	data := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(input)) != 0 {
		if err := json.Unmarshal(input, &data); err != nil {
			return nil, fmt.Errorf("%w: input must be a JSON object: %v", ErrInvalidAction, err)
		}
		if data == nil {
			return nil, fmt.Errorf("%w: input must be a JSON object", ErrInvalidAction)
		}
	}
	if _, exists := data["entity_id"]; exists {
		return nil, fmt.Errorf("%w: input cannot override resolved entity_id", ErrInvalidAction)
	}
	encodedEntity, _ := json.Marshal(entityID)
	data["entity_id"] = encodedEntity
	return json.Marshal(data)
}

func readLimited(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseSize {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

var (
	_ habio.Provider = (*Provider)(nil)
	_ habio.Resolver = (*Provider)(nil)
	_ habio.Observer = (*Provider)(nil)
)
