package modeline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cockroachdb/errors"
)

// splitModelineParts splits the modeline body (everything after the
// `# talm: ` prefix) into `key=value` tokens. Splits on `,` only at
// JSON nesting depth 0, so a comma inside a `nodes=["a", "b"]` array
// no longer cuts the value mid-stream. Tracks `[`/`]` depth and `"`
// string state with JSON-style backslash escapes; a `,` or `]` inside
// a JSON string literal is treated as data, not structure.
//
// This is more permissive than the old `, ` literal split — both the
// canonical talm-generated form (no whitespace inside arrays) and the
// human-written form (whitespace after each array element) now parse.
// Whitespace AROUND tokens is trimmed by the caller's SplitN step.
//
// Scope: JSON-array values only. The splitter does NOT track `{`/`}`
// nesting because every modeline key in the current contract (nodes,
// endpoints, templates) is a JSON array — a `{` at depth 0 will fall
// through to the downstream json.Unmarshal which rejects non-array
// inputs. If a future modeline key takes a JSON-object value, extend
// the depth counter to track `{`/`}` too.
func splitModelineParts(content string) []string {
	parts := make([]string, 0)
	depth := 0
	inString := false
	escape := false
	start := 0

	for i := range len(content) {
		c := content[i]

		if inString {
			switch {
			case escape:
				escape = false
			case c == '\\':
				escape = true
			case c == '"':
				inString = false
			}

			continue
		}

		switch c {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, content[start:i])
				start = i + 1
			}
		}
	}

	parts = append(parts, content[start:])

	return parts
}

// Config structure for storing settings from modeline.
type Config struct {
	Nodes     []string
	Endpoints []string
	Templates []string
}

// ErrModelineNotFound is the sentinel cause FindAndParseModeline
// returns (wrapped with a hint) when the input file has no
// `# talm: …` line at all. Distinct from "found but malformed":
// callers route the not-found case onto a different path (e.g.
// direct-patch in apply, "this isn't a modelined node file" in
// completion) while malformed-modeline errors bubble up so the
// operator sees their typo. Match with errors.Is.
var ErrModelineNotFound = errors.New("modeline not found")

// ParseModeline parses a modeline string and populates the Config structure.
func ParseModeline(line string) (*Config, error) {
	config := &Config{}
	trimLine := strings.TrimSpace(line)

	prefix := "# talm: "
	if after, ok := strings.CutPrefix(trimLine, prefix); ok {
		content := after

		for _, part := range splitModelineParts(content) {
			keyVal := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(keyVal) != 2 {
				//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
				return nil, errors.WithHintf(
					errors.Newf("invalid format of modeline part: %s", part),
					"expected key=value form (value is a JSON array); see modeline contract",
				)
			}

			key := keyVal[0]
			val := keyVal[1]

			var arr []string

			err := json.Unmarshal([]byte(val), &arr)
			if err != nil {
				//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
				return nil, errors.WithHintf(
					errors.Wrapf(err, "error parsing JSON array for key %s, value %s", key, val),
					"value must be a JSON array, e.g. nodes=[\"1.2.3.4\"]",
				)
			}
			// Assign values to Config fields based on known keys
			switch key {
			case "nodes":
				config.Nodes = arr
			case "endpoints":
				config.Endpoints = arr
			case "templates":
				config.Templates = arr
				// Ignore unknown keys
			}
		}

		return config, nil
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
	return nil, errors.WithHint(
		errors.New("modeline prefix not found"),
		"first line must begin with '# talm: '",
	)
}

// FindAndParseModeline scans a file for the talm modeline, allowing
// operator-authored comment lines (`^#`) and blank lines as a leading
// prefix. The first non-comment, non-blank line must be the modeline
// itself (`# talm: …`); arbitrary YAML or prose before the modeline
// is rejected.
//
// Returns the leading comment / blank lines verbatim (without trailing
// `\n`), the parsed Config, and any error. `talm template -I` uses
// the leading-lines return to preserve operator documentation when
// the in-place rewrite overwrites the file. Every other talm
// workflow that consumes node files (apply, upgrade, completion,
// wrapped talosctl commands) calls this function too so the
// file-shape contract is uniform across the surface.
func FindAndParseModeline(filePath string) ([]string, *Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
		return nil, nil, errors.WithHintf(
			errors.Wrap(err, "error opening config file"),
			"check that %s exists and is readable", filePath,
		)
	}
	defer func() { _ = file.Close() }()

	var leading []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)

		// Blank line or a non-modeline comment: keep collecting.
		if trim == "" || (strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, "# talm:")) {
			leading = append(leading, line)

			continue
		}

		// Modeline candidate.
		if strings.HasPrefix(trim, "# talm:") {
			config, parseErr := ParseModeline(line)
			if parseErr != nil {
				// ParseModeline already wraps + WithHint at its boundary.
				return nil, nil, parseErr
			}

			return leading, config, nil
		}

		// First non-comment, non-blank line is not a modeline.
		// Distinguish orphan (no modeline anywhere) from misplaced
		// modeline (a `# talm:` line lives below YAML) via lookahead.
		return nil, nil, classifyNoLeadingModeline(scanner, line)
	}

	if scanErr := scanner.Err(); scanErr != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
		return nil, nil, errors.WithHint(
			errors.Wrap(scanErr, "error reading config file"),
			"file may be truncated or unreadable",
		)
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
	return nil, nil, errors.WithHint(
		ErrModelineNotFound,
		"per-node values file must contain a `# talm: nodes=[…]` modeline; comments and blanks may precede it but at least one modeline must be present",
	)
}

// classifyNoLeadingModeline picks between the "orphan" and
// "misplaced modeline" outcomes when FindAndParseModeline has hit
// a non-comment line without first seeing a `# talm:` modeline.
// Scans the rest of the file: a later `# talm:` line means the
// operator put the modeline below YAML (rejected with a clear
// hint); no `# talm:` anywhere means the file is a legitimate
// orphan (returned as ErrModelineNotFound so callers can route to
// the side-patch / direct-patch path).
func classifyNoLeadingModeline(scanner *bufio.Scanner, firstNonComment string) error {
	for scanner.Scan() {
		// Column-0 prefix only — the canonical modeline always
		// sits at the very start of the line. Indented `# talm:`
		// text inside a YAML body is an operator-authored
		// comment (e.g. "# talm: see the modeline above for
		// nodes/templates wiring"), NOT a misplaced modeline.
		// TrimSpace-then-HasPrefix would false-positive on those
		// and block legitimate node files.
		if strings.HasPrefix(scanner.Text(), "# talm:") {
			//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
			return errors.WithHint(
				errors.Newf("modeline found below non-comment content: first non-comment line was %q", firstNonComment),
				"the `# talm: …` modeline must precede any YAML content; only `#`-prefixed comments and blank lines may sit above it",
			)
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
		return errors.WithHint(
			errors.Wrap(scanErr, "error reading config file"),
			"file may be truncated or unreadable",
		)
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
	return errors.WithHint(
		ErrModelineNotFound,
		"per-node values file must contain a `# talm: nodes=[…]` modeline; comments and blanks may precede it but at least one modeline must be present",
	)
}

// GenerateModeline creates a modeline string using JSON formatting for values.
func GenerateModeline(nodes, endpoints, templates []string) (string, error) {
	// Convert Nodes to JSON
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal nodes")
	}

	// Convert Endpoints to JSON
	endpointsJSON, err := json.Marshal(endpoints)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal endpoints")
	}

	// Convert Templates to JSON
	templatesJSON, err := json.Marshal(templates)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal templates")
	}

	// Form the final modeline string
	modeline := fmt.Sprintf(`# talm: nodes=%s, endpoints=%s, templates=%s`, string(nodesJSON), string(endpointsJSON), string(templatesJSON))

	return modeline, nil
}
