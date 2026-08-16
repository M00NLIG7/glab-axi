package product

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/output"
)

type Parsed struct {
	Definition  Definition
	Values      map[string]string
	Booleans    map[string]bool
	Positionals []string
	Format      output.Format
	Limit       int
}

type ParseResult struct {
	Command *Parsed
	Help    string
}

var deniedTop = map[string]string{
	"api":      "generic API authority is permanently outside glab-axi",
	"workflow": "GitLab uses pipelines and jobs; workflow is not a safe GitLab alias",
	"secret":   "secret values are outside glab-axi's metadata boundary",
	"variable": "GitLab variable responses can expose values and are outside this release",
}

var deniedNested = map[string]map[string]string{
	"auth": {
		"token": "credential display", "show-token": "credential display",
	},
	"mr": {
		"approve": "approval", "comment": "commenting", "note": "commenting",
		"close": "closing", "reopen": "reopening", "delete": "deletion",
	},
	"issue": {
		"create": "issue creation", "edit": "issue editing", "update": "issue editing", "comment": "commenting",
		"note": "commenting", "close": "closing", "reopen": "reopening", "delete": "deletion",
	},
	"pipeline": {
		"run": "pipeline triggering", "trigger": "pipeline triggering", "retry": "pipeline retry",
		"cancel": "pipeline cancellation", "delete": "pipeline deletion",
	},
	"repo": {
		"create": "repository creation", "edit": "repository editing", "update": "repository editing",
		"fork": "repository forking", "delete": "repository deletion", "transfer": "repository transfer",
	},
	"release": {
		"create": "release creation", "edit": "release editing", "update": "release editing",
		"delete": "release deletion", "upload": "release asset mutation",
	},
	"label": {
		"create": "label creation", "edit": "label editing", "update": "label editing", "delete": "label deletion",
	},
	"job": {
		"retry": "job retry", "cancel": "job cancellation", "play": "job mutation", "erase": "job deletion",
	},
}

func Parse(args []string) (ParseResult, error) {
	if len(args) > 0 && args[0] == "help" {
		path := args[1:]
		if len(path) > 2 {
			return ParseResult{}, uxv1.NewError(uxv1.CodeValidation, "help accepts at most a command and subcommand")
		}
		if reason := deniedReason(path); reason != "" {
			return ParseResult{}, uxv1.NewError(uxv1.CodeSecurityBoundary, reason+" is not delegated")
		}
		if help, ok := Help(path); ok {
			return ParseResult{Help: help}, nil
		}
		return ParseResult{}, uxv1.NewError(uxv1.CodeUnsupported, "unknown help topic")
	}

	path, consumed := commandPath(args)
	if reason := deniedReason(path); reason != "" {
		return ParseResult{}, uxv1.NewError(uxv1.CodeSecurityBoundary, reason+" is not delegated")
	}
	if hasHelp(args) {
		if help, ok := Help(path); ok {
			return ParseResult{Help: help}, nil
		}
		return ParseResult{}, uxv1.NewError(uxv1.CodeUnsupported, "unknown command; use glab-axi --help")
	}
	if len(path) == 1 && isParent(path) {
		help, _ := Help(path)
		return ParseResult{Help: help}, nil
	}
	definition, ok := lookupDefinition(path)
	if !ok {
		return ParseResult{}, uxv1.NewError(uxv1.CodeUnsupported, "unknown command; use glab-axi --help")
	}
	parsed, err := parseFlags(definition, args[consumed:])
	if err != nil {
		return ParseResult{}, err
	}
	return ParseResult{Command: &parsed}, nil
}

func commandPath(args []string) ([]string, int) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, 0
	}
	if _, ok := lookupDefinition([]string{args[0]}); ok {
		return []string{args[0]}, 1
	}
	if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
		return []string{args[0], args[1]}, 2
	}
	return []string{args[0]}, 1
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func deniedReason(path []string) string {
	if len(path) == 0 {
		return ""
	}
	if reason := deniedTop[path[0]]; reason != "" {
		return reason
	}
	if len(path) > 1 {
		if children := deniedNested[path[0]]; children != nil {
			if action := children[path[1]]; action != "" {
				return action
			}
		}
	}
	return ""
}

func parseFlags(definition Definition, args []string) (Parsed, error) {
	parsed := Parsed{
		Definition: definition,
		Values:     map[string]string{},
		Booleans:   map[string]bool{},
		Format:     output.TOON,
	}
	if !definition.NoLimit {
		parsed.Limit = 30
	}
	type flagSpec struct {
		canonical string
		value     bool
	}
	specs := map[string]flagSpec{
		"--hostname": {canonical: "--hostname", value: true},
	}
	if strings.Join(definition.Path, " ") != "auth login" {
		if !definition.NoLimit {
			specs["--limit"] = flagSpec{canonical: "--limit", value: true}
		}
		specs["--format"] = flagSpec{canonical: "--format", value: true}
	}
	if definition.RepoMode != RepoNone {
		specs["-R"] = flagSpec{canonical: "--repo", value: true}
		specs["--repo"] = flagSpec{canonical: "--repo", value: true}
	}
	for _, flag := range definition.Flags {
		specs[flag.Name] = flagSpec{canonical: flag.Name, value: !flag.Boolean}
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "-- positional separator is not supported")
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		if strings.HasPrefix(arg, "-") {
			if reason := deniedFlag(definition, name); reason != "" {
				return Parsed{}, uxv1.NewError(uxv1.CodeSecurityBoundary, reason+" is not delegated")
			}
			spec, ok := specs[name]
			if !ok {
				return Parsed{}, uxv1.NewError(uxv1.CodeUnsupported, "unsupported flag: "+name)
			}
			if seen[spec.canonical] {
				return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "duplicate flag: "+spec.canonical)
			}
			seen[spec.canonical] = true
			if !spec.value {
				if hasInline {
					return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "boolean flag does not accept a value: "+name)
				}
				parsed.Booleans[spec.canonical] = true
				continue
			}
			value := inline
			if !hasInline {
				if i+1 >= len(args) {
					return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "missing value for "+name)
				}
				i++
				value = args[i]
			}
			if !validArgument(value) {
				return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "invalid value for "+name)
			}
			parsed.Values[spec.canonical] = value
			continue
		}
		if !validArgument(arg) {
			return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "invalid positional argument")
		}
		parsed.Positionals = append(parsed.Positionals, arg)
	}
	min, max := definition.Positionals, definition.MaxPositions
	if max == 0 {
		max = min
	}
	if len(parsed.Positionals) < min || len(parsed.Positionals) > max {
		return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "unexpected positional arguments")
	}
	if format := parsed.Values["--format"]; format != "" {
		parsed.Format = output.Format(format)
		if parsed.Format != output.TOON && parsed.Format != output.JSON {
			return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "format must be toon or json")
		}
	}
	if raw := parsed.Values["--limit"]; raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "limit must be an integer from 1 through 1000")
		}
		parsed.Limit = limit
	}
	if definition.RequireExplicitHost && parsed.Values["--hostname"] == "" {
		return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "missing required flag: --hostname")
	}
	if definition.RequireExplicitRepo && parsed.Values["--repo"] == "" {
		return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "missing required flag: --repo")
	}
	for _, flag := range definition.Flags {
		if flag.Required && parsed.Values[flag.Name] == "" && !parsed.Booleans[flag.Name] {
			return Parsed{}, uxv1.NewError(uxv1.CodeValidation, "missing required flag: "+flag.Name)
		}
	}
	if err := validateParsedCommand(parsed); err != nil {
		return Parsed{}, err
	}
	return parsed, nil
}

func deniedFlag(definition Definition, name string) string {
	path := strings.Join(definition.Path, " ")
	if path == "auth status" && (name == "--show-token" || name == "-t") {
		return "credential display"
	}
	if path == "auth login" {
		switch name {
		case "--token", "-t", "--job-token", "-j", "--stdin", "--insecure-storage", "--device", "--web":
			return "credential-bearing or policy-bypassing login flags"
		}
	}
	if path == "mr merge" {
		switch name {
		case "--merge", "--rebase", "--method", "--auto", "--auto-merge", "--when-pipeline-succeeds",
			"--delete-branch", "--remove-source-branch", "--message", "-m", "--squash-message",
			"--subject", "--body", "--body-file", "--admin", "--yes", "-y", "-s":
			return "unguarded or alternate merge behavior"
		}
	}
	return ""
}

func validArgument(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
