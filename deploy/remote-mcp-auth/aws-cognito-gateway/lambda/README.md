# Cognito Pre Token Generation Lambda Templates

These files are claim-normalization starters, not authorization systems. They
help DevOps copy JumpCloud or IdP group attributes into Cognito access-token
claims that `gongmcp-gateway` can enforce through `COGNITO_GROUP_CLAIM`.

## When To Use

Use this Lambda when:

- JumpCloud login succeeds, but the Cognito access token is missing the group
  claim configured in `COGNITO_GROUP_CLAIM`.
- SAML/OIDC attribute mapping lands in a user-pool attribute or Cognito group,
  but Cognito does not emit that value in access tokens by default.

Do not use this Lambda as the only access gate. The gateway still validates
issuer, `client_id`, scope, signature, and configured group/subject/email
policy.

## Template

- `pre-token-generation-jumpcloud-groups.py`: stdlib-only Python handler for
  Pre Token Generation trigger source `V2_0+`.

## Operator Steps

1. Map JumpCloud groups into a user-pool attribute, for example
   `custom:jumpcloud_group`, or Cognito groups during SAML/OIDC setup.
2. Create a Lambda function from the template. Set environment variables:
   - `SOURCE_USER_ATTRIBUTE=custom:jumpcloud_group`
   - `TARGET_ACCESS_TOKEN_CLAIM=custom:jumpcloud_groups`
   - optional `INCLUDE_COGNITO_GROUPS=1`
3. Attach the function as a Pre Token Generation trigger on the user pool. Use
   Lambda config version `V2_0` or newer so access-token claims can be
   customized.
4. Set gateway config:

   ```text
   COGNITO_GROUP_CLAIM=custom:jumpcloud_groups
   COGNITO_REQUIRED_GROUP=gongmcp-users
   ```

5. Decode a non-prod access token locally and confirm the target claim is
   present before testing Claude.

## Syntax Check

```bash
python3 -m py_compile deploy/remote-mcp-auth/aws-cognito-gateway/lambda/pre-token-generation-jumpcloud-groups.py
```
