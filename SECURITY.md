# Security

## Sensitive Data

Do not open public issues or pull requests that include:

- Gong access keys or secrets
- OAuth client secrets or refresh tokens
- real transcripts
- real recordings
- customer account data

If sensitive data is committed, rotate the affected credential in Gong and purge the data from git history before making the repository public.

## Supported Auth

For Gong API ingestion, the CLI supports Gong access key and access key secret
through environment variables or a private `.env` file. Native Gong OAuth inside
`gongctl` is not implemented.

For MCP access, local stdio `gongmcp` reads SQLite only and does not need Gong
credentials. HTTP `gongmcp` supports bearer-token auth and explicit Origin
allowlisting for private deployments. Remote MCP OAuth/SSO should be handled by
a customer-managed gateway or broker in front of `gongmcp`; this repository
includes a lab harness that rehearses that gateway pattern, but it is not a
production identity provider configuration.

## Reporting

Report security issues through GitHub private vulnerability reporting:

https://github.com/fyne-coder/gongcli_mcp/security/advisories/new

Do not include customer transcript text or credentials in public issues, pull requests, or discussions.
