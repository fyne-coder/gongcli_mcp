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

The MVP supports Gong access key and access key secret via environment variables. OAuth is intentionally deferred until the CLI flows are stable and the storage boundary is designed.

## Reporting

Report security issues through GitHub private vulnerability reporting:

https://github.com/fyne-coder/gongcli_mcp/security/advisories/new

Do not include customer transcript text or credentials in public issues, pull requests, or discussions.
