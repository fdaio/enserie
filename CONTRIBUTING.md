# Contributing

## Releasing

A release starts when a `v` tag is pushed. The Release workflow builds every
package, publishes them to a GitHub release, and then updates the Homebrew tap.
The tap is a separate repository, so it runs as its own job.

The tap job runs after `publish` on purpose. A tap failure leaves
`brew install fdaio/enserie/ens` on the previous version and says so, instead of
holding a finished release hostage.

Check what the tap currently serves with:

```bash
gh api repos/fdaio/homebrew-enserie/commits --jq '.[0].commit.message'
```

## The tap key

The tap job pushes to `fdaio/homebrew-enserie`, which the workflow cannot reach
with `GITHUB_TOKEN` because that token is scoped to the repository the
workflow runs in. It authenticates with a deploy key instead.

A deploy key has no expiry, reaches one repository only, and stops working the
moment it is deleted. A personal access token has an expiry and a wider blast
radius, which is why this moved off one.

### Create the key

```bash
ssh-keygen -t ed25519 -C "enserie tap deploy key" -f tap_key -N ""
```

The private half goes into the workflow. The public half goes into the tap.
Never commit either half, and never paste the private half into an issue or a
pull request.

### Install the key

Give the public half write access to the tap. A read only key clones the tap
but cannot push the formula.

```bash
gh api -X POST repos/fdaio/homebrew-enserie/keys \
  -f title="enserie release workflow" \
  -f key="$(cat tap_key.pub)" \
  -F read_only=false
```

Then store the private half as a secret of the source repository. Read it from
standard input so it never reaches the shell history.

```bash
gh secret set TAP_SSH_KEY --repo fdaio/enserie < tap_key
```

Confirm the secret exists and delete the key file afterwards.

```bash
gh secret list --repo fdaio/enserie
shred -u tap_key tap_key.pub
```

### Revoke the key

Delete it from the tap. Every release fails at its tap job until a new key is
installed, and the release itself is unaffected.

```bash
gh api -X DELETE repos/fdaio/homebrew-enserie/keys/KEY_ID
gh secret delete TAP_SSH_KEY --repo fdaio/enserie
```

Find the key ID with `gh api repos/fdaio/homebrew-enserie/keys`.

### When the private half is lost

It cannot be recovered, because nothing stores it. Generate a new pair, delete
the old deploy key, and install the new one as above. The workflow only ever
reads the secret, so replacing it needs no commit.

### When the tap job fails

A missing secret and a rejected key look different in the log. Read the failing
step first:

```bash
gh run view RUN_ID --log-failed
```

`TAP_SSH_KEY is not set` means the secret is missing or empty. `Permission
denied (publickey)` means the deploy key on the tap does not match the secret,
or the key is read only. `Host key verification failed` means the host key does
not match the fingerprint GitHub publishes, so stop and do not work around it.

Once the cause is fixed, rerun the failed job instead of cutting another
release. The release artifacts are still in the run:

```bash
gh run rerun RUN_ID --failed
```
