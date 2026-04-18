## Summary

<!-- One or two sentences. What does this PR change and why? -->

## Related ADR / issue

<!--
- Closes #
- Implements ADR-00XX (or supersedes ADR-00XX with a new one in this PR)
-->

## Type of change

- [ ] Bug fix (non-breaking change which fixes an issue)
- [ ] New feature (non-breaking change which adds functionality)
- [ ] Breaking change (API, CRD schema, or security default)
- [ ] Documentation only
- [ ] Refactor / internal cleanup (no user-facing change)

## Test evidence

<!--
Paste relevant output:

  make lint
  make test

If the change is user-facing, describe the manual smoke test
(kind / k3s cluster, `kubectl apply -k config/samples/`, observed
behaviour).
-->

## Checklist

- [ ] `make manifests generate` produces no diff (I ran it).
- [ ] Tests cover the new behaviour.
- [ ] Docs updated if behaviour changed (`docs/architecture.md`,
      `docs/security-model.md`, or an ADR under `docs/adr/`).
- [ ] Commit messages follow Conventional Commits.
