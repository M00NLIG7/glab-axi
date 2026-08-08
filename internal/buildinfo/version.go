package buildinfo

const (
	Version           = "0.2.0"
	UpdateManifestURL = "https://github.com/M00NLIG7/glab-axi/releases/latest/download/glab-axi-update-v1.json"
)

// UpdatePublicKey is injected into release artifacts as base64 Ed25519 public
// key material. Development builds intentionally cannot self-update.
var UpdatePublicKey string
