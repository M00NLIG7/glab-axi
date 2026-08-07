package cli

import (
	"strings"

	"glab-axi/internal/contract/v1"
)

type flagSpec map[string]bool // true means the flag consumes a value

type parsedArgs struct {
	values      map[string]string
	booleans    map[string]bool
	positionals []string
}

func parseStrict(args []string, spec flagSpec, minPositionals, maxPositionals int) (parsedArgs, error) {
	result := parsedArgs{values: map[string]string{}, booleans: map[string]bool{}}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			if arg == "--" || strings.Contains(arg, "=") {
				return parsedArgs{}, v1.NewError(v1.CodeValidation, "unsupported flag syntax")
			}
			needsValue, ok := spec[arg]
			if !ok {
				return parsedArgs{}, v1.NewError(v1.CodeUnsupported, "unsupported flag: "+arg)
			}
			if seen[arg] {
				return parsedArgs{}, v1.NewError(v1.CodeValidation, "duplicate flag: "+arg)
			}
			seen[arg] = true
			if needsValue {
				if i+1 >= len(args) {
					return parsedArgs{}, v1.NewError(v1.CodeValidation, "missing value for "+arg)
				}
				i++
				value := args[i]
				if value == "" || strings.ContainsRune(value, '\x00') {
					return parsedArgs{}, v1.NewError(v1.CodeValidation, "invalid value for "+arg)
				}
				result.values[arg] = value
			} else {
				result.booleans[arg] = true
			}
			continue
		}
		if strings.ContainsRune(arg, '\x00') {
			return parsedArgs{}, v1.NewError(v1.CodeValidation, "invalid positional argument")
		}
		result.positionals = append(result.positionals, arg)
	}
	if len(result.positionals) < minPositionals || len(result.positionals) > maxPositionals {
		return parsedArgs{}, v1.NewError(v1.CodeValidation, "unexpected positional arguments")
	}
	return result, nil
}

func (p parsedArgs) require(flags ...string) error {
	for _, flag := range flags {
		if p.values[flag] == "" && !p.booleans[flag] {
			return v1.NewError(v1.CodeValidation, "missing required flag: "+flag)
		}
	}
	return nil
}
