# Releasing

A maintainer releases by pushing a tag, and the tag is the whole release
(D-90). `release.yml` runs the tests, takes the tag's CHANGELOG section
as the notes, and builds with GoReleaser. It publishes a release, never
a draft. The archives, their SBOMs and the `.mcpb` bundle are in the
cosign-signed `checksums.txt` and carry build provenance (D-91, D-93).
Last, `publish-mcp.yml` publishes the MCP registry entry, except for a
prerelease tag.

A finished piece of work ends in a tagged release, and the tag waits for
the owner's explicit go.

## Before the release commit

- `make check` passes, and CI is green on Linux, macOS and Windows
  (D-85, D-87).
- `make live` has run for anything touching the write path or an API
  response shape, with `GDOCS_ENABLE_DESTRUCTIVE=true` when the release
  touches the delete tools, and its transcript was read (D-83). The
  evals have run if the release changes what a model reads (D-84).
- `/simplify`, `/code-review medium` and `/security-review` have run
  since the previous tag.
- `make api-diff` and `make schema-refetch`, which reach the network and
  so are not in `make check`. `api-diff` rewrites
  `testdata/api-methods.json` and `testdata/api-fields.json`, and
  anything Google added fails `make api-coverage` or `make api-fields`
  until its TSV gives it a verdict (D-09). `schema-refetch` checks the
  vendored schemas in `scripts/gates/schemas` against their sources.
- Scan every blob ever committed for identifiers, which `make check`
  does not; a file removed from the tip is still in the history (D-06):

  ```bash
  LEAKCHECK_HISTORY=1 go test ./scripts/gates -run TestHistoryCarriesNoIdentifiers
  ```

- Rehearse the build into `dist/`, publishing nothing, with the
  GoReleaser version `release.yml` pins. It skips signing, which needs
  the workflow's identity, and SBOMs, since syft is installed only in
  CI:

  ```bash
  go run github.com/goreleaser/goreleaser/v2@v2.18.1 release --snapshot --clean --skip=sign,sbom,publish
  ```

  The version must agree in the bundle's and the archives' file names,
  `--version` and `checksums.txt`. A bundle missing from
  `checksums.txt` ships unsigned and looks no different.

## Steps

1. On a topic branch, rename `[Unreleased]` in `CHANGELOG.md` to
   `## [X.Y.Z] - YYYY-MM-DD` and leave an empty `[Unreleased]` above it.
   `make staleness` accepts entries under a version with no tag yet.
2. Read `make schema-diff`. A break of the last release's surface fails
   unless a `**Breaking:**` entry names it, and ships only in a new major
   version, with go.mod's `/vN` raised (D-77).
3. Commit as `Release X.Y.Z`, open a pull request, and merge once CI is
   green on that commit (D-92).
4. Tag the merge, annotated, and push the tag on its own:

   ```bash
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

   GitHub creates no tag events past the third in one push. If the
   release never starts, run `release.yml` by hand on the tag.

## What no rehearsal reaches

Signing, attestation and the registry publish need the workflow's OIDC
token, so read the first run after any change to the pipeline.

| Fails | State afterward | Recovery |
|---|---|---|
| tests, release notes, cosign or SBOMs | no release | fix it, then release the next patch |
| attestation | release published, unattested | fix it, then release the next patch |
| registry publish | release fine, no entry | `gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z`; do not tag again |

## Verify from outside

```bash
cd "$(mktemp -d)" && gh release download vX.Y.Z --repo mmedum/google-docs-mcp
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity 'https://github.com/mmedum/google-docs-mcp/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-docs-mcp_X.Y.Z_linux_amd64.tar.gz --repo mmedum/google-docs-mcp
gh attestation verify google-docs-mcp_X.Y.Z.mcpb --repo mmedum/google-docs-mcp
```

A pass prints little, so read the exit code. Last, run the README's
`go install …@latest` command outside the repository, as a user would,
and check that `google-docs-mcp --version` names the new release.

## A bad release

A bad release is fixed forward.

- Never move or delete a pushed tag. The Go module proxy caches it
  within minutes, and the checksum database records its hash.
- Ship a patch release with the fix.
- In that patch, add `retract vX.Y.Z // why` to `go.mod`, so the go
  command skips the bad version for `@latest`.
- Mark the bad version's CHANGELOG heading `[YANKED]`:
  `## [X.Y.Z] - YYYY-MM-DD [YANKED]`.
