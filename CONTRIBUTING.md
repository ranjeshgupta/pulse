# Contributing to Pulse

Thank you for helping. Bug reports, fixes, documentation, tests and new provider adapters are all
welcome. Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md). This page is the short version; the full guide, with the repository layout, every make
target and the test setup, is [Contributing and testing](docs/developer/08-contributing-and-testing.md).

## Reporting bugs and asking for features

- Search the existing issues first.
- For a bug, say what you did, what you expected and what happened, with the Pulse commit, the
  roles you run (`pulse all` or separate roles) and the relevant log lines (`make logs` on the local
  stack). Remove personal data, API keys and secrets first.
- For a feature, describe the problem you want to solve before the solution. Check the known gaps
  in the [product overview](docs/product.md#honest-limits-at-a-glance) and the
  [architecture document](docs/architecture.md#21-known-limits-and-trade-offs) first.
- **Security problems:** don't open a public issue. Follow [SECURITY.md](SECURITY.md).

## Setting up

You need Docker (with Compose v2) and `make`. Go and Node.js on the host are optional: without Go,
the make targets run it in the `golang:1.26-alpine` image. Node.js 22.12 or later is needed for
`make docs-check` and for the web app's dev server and tests.

```bash
make up        # the full local stack, with Pulse at http://localhost:7080
make seed      # a demo workspace with data (sign-in details: deploy/dev-seed.md)
```

Details and troubleshooting: [Installation](docs/developer/01-installation.md).

## Making a change

1. Fork the repository and create a branch for your change.
2. Keep the change focused: one fix or feature per pull request.
3. Follow the conventions in the code (summarised in
   [Conventions in the Go code](docs/developer/08-contributing-and-testing.md#conventions-in-the-go-code)):
   standard library first, the tenant ID on every store call, idempotent Kafka consumers, error
   messages people can act on, and comments that explain why.
4. Add or update tests at the right level: unit tests for logic, `make it` where Go and ClickHouse
   must agree, `make e2e` for behaviour across roles.
5. Update the docs in the same change. The table in
   [Keeping the docs in sync](docs/developer/08-contributing-and-testing.md#keeping-the-docs-in-sync)
   says which document covers what; if the UI changes, retake that guide's screenshots with
   `make docs-shots SHOT=<guide>`.

## Before you open a pull request

```bash
make vet test                                # Go vet and unit tests
cd web && npm run typecheck && npm test      # web app type check and tests
cd .. && make docs-check                     # links, images and anchors in the docs
make it e2e                                  # with the stack running (make up)
```

In the pull request, describe what changed and why, how you tested it, and anything a reviewer
should look at closely. Screenshots help for UI changes.

## Licensing of contributions

The platform is licensed under the [GNU AGPL v3.0](LICENSE) and the SDKs under the MIT License (see
the [License section of the README](README.md#license)). By sending a contribution you agree that it
is licensed under the license of the part of the repository it changes.
