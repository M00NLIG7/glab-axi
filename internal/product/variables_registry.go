package product

func variableDefinitions() []Definition {
	var out []Definition
	for _, group := range []string{"secret", "variable"} {
		for _, action := range []string{"list", "set", "delete"} {
			flags := []FlagDefinition{{Name: "--scope", Value: "SCOPE", Description: "Exact GitLab environment_scope, including literal *; never environment matching.", Required: true}}
			d := Definition{Path: []string{group, action}, Summary: "Manage project CI/CD " + group + " metadata with exact-scope guards.", Details: "Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.\nRequires GitLab 17.6 or newer. Lists never emit values or descriptions.\nsecret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.\nNo group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.", Usage: "gl-axi " + group + " " + action + " --auth-source native [global flags] --scope SCOPE", RepoMode: RepoRequired, Schema: "ci-variable-list", Backend: "native", NativeAuth: true, RequireNativeAuth: true, RequireExplicitHost: true, RequireExplicitRepo: true}
			if action != "list" {
				d.Positionals = 1
				d.MaxPositions = 1
				d.Write = true
				d.NoLimit = true
				d.Schema = "ci-variable-mutation"
				d.Usage = "gl-axi " + group + " " + action + " KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]"
				d.Details += "\nUnavailable on Windows until private-file ACL verification is supported.\nOne mutation, no retry; preflight is not atomic CAS. Updates preserve type and protection.\nExisting entries require exact class/type/protected/raw and a private expected-value file.\nsecret set creates hidden+masked entries or rotates existing hidden entries, never silently promotes masked/unhidden entries."
				flags = append(flags,
					FlagDefinition{Name: "--expected-project-id", Value: "ID", Description: "Exact positive project ID.", Required: true},
					FlagDefinition{Name: "--expected-project-url", Value: "URL", Description: "Exact HTTPS project URL.", Required: true},
					FlagDefinition{Name: "--expected-class", Value: "CLASS", Description: "absent (set only), ordinary, protected, masked, or hidden.", Required: true},
					FlagDefinition{Name: "--expected-type", Value: "TYPE", Description: "Existing env_var or file type."},
					FlagDefinition{Name: "--expected-protected", Value: "BOOL", Description: "Existing true or false protection."},
					FlagDefinition{Name: "--expected-raw", Value: "BOOL", Description: "Existing true or false raw expansion setting."},
					FlagDefinition{Name: "--expected-value-file", Value: "FILE", Description: "Private absolute file containing exact previous value; never a hash or argv value."},
					FlagDefinition{Name: "--confirm", Boolean: true, Required: true, Description: "Explicitly authorize this exact guarded mutation."})
				if action == "set" {
					flags = append(flags,
						FlagDefinition{Name: "--value-file", Value: "FILE|-", Description: "Private absolute file or piped stdin; UTF-8, no NUL, 1..10000 bytes, no newline trimming.", Required: true},
						FlagDefinition{Name: "--type", Value: "TYPE", Description: "env_var or file. Existing type must be preserved.", Required: true},
						FlagDefinition{Name: "--protected", Value: "BOOL", Description: "true or false; variable requires false; existing protection must be preserved.", Required: true})
				}
			}
			d.Flags = flags
			out = append(out, d)
		}
	}
	return out
}
