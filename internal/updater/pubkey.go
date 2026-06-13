package updater

// minisignPublicKey is the pinned minisign public key used to verify the
// signature over each release's checksums.txt. It is the base64 string that
// `minisign -G` prints after "Public key:".
//
// When this is set, FetchVerifiedBinary REFUSES any release whose checksums.txt
// is not accompanied by a valid checksums.txt.minisig — so write access to a
// GitHub release alone is no longer enough to ship a trojaned `ctx` binary; an
// attacker would also need the offline signing key. Until it is set, the updater
// falls back to checksum-only verification (the prior behavior) so existing
// unsigned releases keep working.
//
// One-time setup to enable signing end to end:
//  1. Generate a password-less keypair (password-less so CI can sign
//     non-interactively):
//       minisign -G -W -p minisign.pub -s minisign.key
//  2. Paste the "Public key:" base64 value below (and into scripts/install.sh's
//     MINISIGN_PUBKEY for parity).
//  3. Add the contents of minisign.key as the GitHub Actions secret MINISIGN_KEY
//     (keep minisign.key itself OFF any repo / cloud sync). The release workflow
//     and .goreleaser.yaml `signs:` block do the rest.
const minisignPublicKey = ""
