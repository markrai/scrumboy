# MCP and Agora integration

Automation surfaces share store-backed tools but **not** the same bearer credential model. Only canonical `POST /mcp/rpc` accepts Scrumboy OAuth access tokens (resource-bound to `<origin>/mcp/rpc`). Legacy `/mcp` and Agora deliberately do not.

```mermaid
flowchart TB
  Client[Automation client]
  Client --> Entry{entry point}

  Entry --> McpRpc["POST /mcp/rpc JSON-RPC"]
  Entry --> McpRoot["GET/POST /mcp legacy"]
  Entry --> AgoraD["POST /agora/v1/discover"]
  Entry --> AgoraI["POST /agora/v1/invoke"]

  McpRpc --> AuthRpc["resolveRequestAuth allowOAuth"]
  McpRoot --> AuthLeg["resolveRequestAuth allowOAuth false"]
  AgoraD --> Strip["mcp.WithoutOAuthBearer then internal /mcp/rpc"]
  AgoraI --> Strip

  AuthRpc --> CredRpc["session cookie OR sb_ API token OR OAuth access token"]
  AuthLeg --> CredLeg["session cookie OR sb_ API token only"]
  Strip --> AuthRpc

  CredRpc --> Chal["401 WWW-Authenticate Bearer resource_metadata mcp/rpc"]
  CredRpc --> Adapter[mcp.Adapter tools]
  CredLeg --> Adapter
  Adapter --> Store[store authz same as REST]
```

| Surface | Session cookie | `sb_…` API token | OAuth access token | Protected-resource challenge |
|---------|---------------:|-----------------:|-------------------:|-----------------------------:|
| Canonical `POST /mcp/rpc` | yes | yes | yes | yes |
| Legacy `/mcp` | yes | yes | no | no |
| `/agora/v1/*` | yes | yes | no (`WithoutOAuthBearer`) | no |

OAuth access tokens are **not** portable to legacy MCP or Agora. Tools are registered in `internal/mcp`; mode (`full` vs `anonymous`) gates which operations are exposed. Agora wraps MCP tool discovery and invocation for Agoragentic-compatible clients.
