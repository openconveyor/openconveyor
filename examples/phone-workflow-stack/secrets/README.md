# Secret files

Put three files in this directory. Git ignores them. Each file holds
one value, with mode `600` and no trailing newline.

| File | Secret | Namespace | Value |
|---|---|---|---|
| `anthropic-api-key` | `anthropic-api-key` (key `api-key`) | `conveyor-tasks` | The Anthropic API key |
| `github-token` | `github-token` (key `token`) | `conveyor-tasks` | The fine-grained PAT for the target repository |
| `github-webhook-secret` | `github-webhook` (key `secret`) | `conveyor-system` | The HMAC key of the GitHub webhooks, 32 random bytes as hex |

Write a value without a newline:

```sh
umask 077
printf '%s' "$(openssl rand -hex 32)" > github-webhook-secret
```

`../scripts/check-secrets.sh` checks the three files. `make check`
and `make install` run it first.
