---
description: "Trigger for git commits, staging, pushing, PRs, or commit messages."
---

## Git Safety
- Leak prevention: Zero passwords, certs, IPs, secrets, credentials, PII, or confidential data.
- Git history: Never scrape or scan historical logs.
- Env files: Force gitignore for `.env`. Never git add.
- Sample envs: Only git add `.env.sample`. Enforce mock/sample values only.
