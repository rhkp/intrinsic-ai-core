## Deployment Fixes
- Consult upstream before fixing deployment. No blind fixes.
- Apply targeted fixes only. Minimize lines/files changed.
- Zero regressions. Maintain existing functionality; run tests.
- Be structured. Always be in know of bottom up services. First ensure the most independent service is fixed, if needed, prove it working and then go up the chain. This ensures we have solid foundation to work.
- Ensure you know the services, ports, protocols being used by each service, so you can consult the truth list when making code changes.
- Don't shoot in dark or guess or hallucinate, but always refer to ground data to carry the fixes.