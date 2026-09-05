# Habio architecture

## System boundary

```text
probabilistic caller
        |
   adapter (MCP/API)
        |
        v
+-----------------------+
| Habio execution core  |
|                       |
| admit -> attempt      |
| dispatch -> record    |
| observe -> verify     |
+-----------+-----------+
            |
       provider contract
            |
   hardware platform
            |
     physical system
```

The adapters and providers are edges. The core knows neither MCP nor Home
Assistant. A command path and an observation path may use different providers;
their evidence meets at the execution boundary rather than inside a device
object.

## Logical components

### Core

Defines Actions, attempts, outcome knowledge, observations, verification input
and result, lifecycle facts, and narrow extension contracts. It coordinates
these concepts but does not discover devices, interpret natural language, or
choose a domain policy.

### Adapters

Translate caller protocols into Actions and expose the resulting evidence. The
first MCP adapter is expected to offer a small surface such as `list_targets`,
`get_state`, and `execute_action`; it must not claim physical success when Habio
reports an unknown or unverified result.

### Providers

Absorb protocol, vendor, and hardware-platform complexity. Home Assistant is the
first proof-of-concept provider because it already owns integration, entity,
and device concerns. MHS, ROS2, OpenHAB, and custom systems can be peer providers.

### Policy extensions

Resolvers, Admitters, Observers, Verifiers, and EventSinks attach through small
contracts. Concrete authorization and safety policy remains outside the core.

## Local-first deployment

```text
local app or local AI
        |
 self-hosted Habio server
        |
 local provider (for example Home Assistant)
        |
     physical device
```

The execution path must work with no Habio cloud dependency. Cloud products may
add managed connectivity and operations but cannot be a prerequisite for local
execution.

## Repository architecture

Start with one repository, not the final ecosystem topology.

### `habiohq/habio` (now)

- core Go library;
- execution semantics and contracts;
- design documents and RFCs; and
- early conformance examples.

The first server and Home Assistant proof of concept may initially be developed
alongside the core if that is the fastest way to test semantics. They should not
be imported into the core package.

### `habiohq/habio-server` (when runtime work starts to move independently)

- self-hosted runtime and CLI;
- local API, configuration, and storage;
- MCP adapter and reference UI; and
- initially, the Home Assistant provider.

### Later repositories

Provider, MCP, strategy, and conformance repositories are created only when an
independent release cycle or maintainer boundary is demonstrated. Likely
candidates include `habio-provider-homeassistant`, `habio-provider-mhs`,
`habio-mcp`, `habio-strategy-sdk`, and `habio-conformance`.

The split rule is:

> Independent release cycle means separate repository.

An implementation that a third party can replace is also a candidate to live
outside core, but replaceability alone does not require an immediate split.

## OSS and SaaS boundary

The product principle is **open execution, managed convenience**.

Apache-2.0 OSS includes the execution core, self-hosted runtime, provider
contracts, local MCP, local execution, local strategy runtime, and extension
SDKs. A proprietary Habio Cloud may provide hosted MCP, OAuth, remote
connectivity, credential management, fleet operations, history, observability,
audit, policy UI, integration registry, marketplace, billing, teams, and
support.

Commercial value comes from managed operations, not from disabling local
execution or withholding its semantics.

## Dependency rules

- `core` must not import adapter, provider, device-model, or cloud packages.
- adapters may depend on core contracts, never the reverse.
- providers may depend on core contracts and vendor clients, never the reverse.
- strategy runtimes may produce Actions but cannot add strategy concepts to core.
- storage is behind an event/fact contract and is not the source of semantic
  truth.

These rules should be enforced by package layout and tests after the first
public contracts are accepted through RFCs.
