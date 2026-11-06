# Instructions for AI Agents

- Before creating any contribution, read [AI_POLICY.md](AI_POLICY.md) and follow it.
- From the repository root, build with `go build ./cmd/restic`. Run `go test ./...` before finishing a change.
- Before committing Go changes, run `gofmt -w **/*.go` and `golangci-lint run` from the repository root.
- For in-depth debugging, profiling, and test layout, read the "Debug Logs", "Debug Hanging", "Debugging", "Performance and Memory Usage Issues", and "Writing tests" sections of [doc/090_participating.rst](doc/090_participating.rst).
- Do not edit `doc/man` or the generated completion files `doc/*-completion.*`. For a relevant enough user-visible change, add a file in `changelog/unreleased/` using `changelog/TEMPLATE`. Documentation-only changes get no changelog entry.
- Follow "Git Commits" in [CONTRIBUTING.md](CONTRIBUTING.md): terse subject, blank line, body, and no merge commits.
- Refer to our [contributing guide](CONTRIBUTING.md) for creating issues and pull requests.
- For a possible critical security problem, follow [Security](CONTRIBUTING.md#security).
