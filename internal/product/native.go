package product

import (
	"context"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/productnative"
	"gl-axi/internal/safeurl"
)

func validateNativeSelection(parsed Parsed) error {
	source, provided := parsed.Values["--auth-source"]
	if !provided {
		return nil
	}
	if !parsed.Definition.NativeAuth || source != "native" {
		return uxv1.NewError(uxv1.CodeValidation, "auth-source must be native on a declared native-capable command")
	}
	if err := safeurl.ValidateHost(parsed.Values["--hostname"]); err != nil {
		return uxv1.NewError(uxv1.CodeValidation, "native auth requires an explicit valid --hostname")
	}
	if parsed.Definition.RepoMode != RepoNone {
		if err := safeurl.ValidateProject(parsed.Values["--repo"]); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "native auth requires an explicit valid --repo")
		}
	}
	return nil
}

func openNative(ctx context.Context, parsed Parsed, deps Dependencies) (*productnative.Client, error) {
	if parsed.Values["--auth-source"] != "native" || !parsed.Definition.NativeAuth {
		return nil, uxv1.NewError(uxv1.CodeValidation, "explicit --auth-source native is required")
	}
	if err := validateNativeSelection(parsed); err != nil {
		return nil, err
	}
	path, err := deps.Runtime.ConfigFile()
	if err != nil {
		return nil, uxv1.AsError(err)
	}
	return productnative.Open(ctx, productnative.Options{
		Host: parsed.Values["--hostname"], ConfigPath: path,
		LookupEnv: deps.Runtime.LookupEnv, Keyring: deps.Runtime.Keyring,
		HTTPClient: deps.Runtime.HTTPClient,
	})
}
