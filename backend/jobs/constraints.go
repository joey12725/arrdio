package jobs

import (
	"context"
	"encoding/json"
	"strings"
)

// Constraints are opt-in hard requirements. Empty fields retain permissive defaults.
type Constraints struct {
	Language  string `json:"language,omitempty"`
	Narrator  string `json:"narrator,omitempty"`
	Edition   string `json:"edition,omitempty"`
	Container string `json:"container,omitempty"`
}

func (in Input) MarshalJSON() ([]byte, error) {
	type plain Input
	var c *Constraints
	if !in.Constraints.Empty() {
		copy := in.Constraints
		c = &copy
	}
	return json.Marshal(struct {
		plain
		Constraints *Constraints `json:"constraints,omitempty"`
	}{plain(in), c})
}
func (c Constraints) Empty() bool {
	return c.Language == "" && c.Narrator == "" && c.Edition == "" && c.Container == ""
}
func (c Constraints) scope() string {
	if c.Empty() {
		return ""
	}
	raw, _ := json.Marshal(c)
	return ":constraints:" + tokenHash(string(raw))
}
func normalizeConstraint(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
func LanguageIdentity(s string) string {
	switch normalizeConstraint(s) {
	case "en", "eng", "english":
		return "en"
	case "es", "spa", "spanish":
		return "es"
	case "ja", "jpn", "japanese":
		return "ja"
	case "fr", "fra", "fre", "french":
		return "fr"
	case "de", "deu", "ger", "german":
		return "de"
	case "it", "ita", "italian":
		return "it"
	case "nl", "nld", "dut", "dutch":
		return "nl"
	case "da", "dan", "danish":
		return "da"
	case "pt", "por", "portuguese":
		return "pt"
	case "ru", "rus", "russian":
		return "ru"
	case "zh", "zho", "chi", "chinese":
		return "zh"
	case "ko", "kor", "korean":
		return "ko"
	case "sv", "swe", "swedish":
		return "sv"
	case "pl", "pol", "polish":
		return "pl"
	}
	return normalizeConstraint(s)
}
func LanguageMatches(want, actual string) bool {
	w, a := LanguageIdentity(want), LanguageIdentity(actual)
	return w != "" && a != "" && (w == a || (!strings.Contains(w, "-") && strings.HasPrefix(a, w+"-")))
}
func (in Input) Allows(c Candidate, format string) bool {
	match := func(want, actual string) bool {
		return want == "" || (actual != "" && normalizeConstraint(want) == normalizeConstraint(actual))
	}
	if (in.Constraints.Language != "" && !LanguageMatches(in.Constraints.Language, c.Language)) || !match(in.Constraints.Edition, c.Edition) {
		return false
	}
	if format == "audio" && !match(in.Constraints.Narrator, c.Narrator) {
		return false
	}
	return match(strings.TrimPrefix(in.Constraints.Container, "."), strings.TrimPrefix(c.Extension, "."))
}

// RequirementState distinguishes a proven incompatible item from missing facts.
// Unknown never overrides a known contradiction on another required field.
func (in Input) RequirementState(c Candidate, format string) string {
	unknown := false
	fields := [][2]string{{in.Constraints.Language, c.Language}, {in.Constraints.Edition, c.Edition}, {strings.TrimPrefix(in.Constraints.Container, "."), strings.TrimPrefix(c.Extension, ".")}}
	if format == "audio" {
		fields = append(fields, [2]string{in.Constraints.Narrator, c.Narrator})
	}
	for index, pair := range fields {
		if pair[0] == "" {
			continue
		}
		if pair[1] == "" {
			unknown = true
			continue
		}
		want, actual := normalizeConstraint(pair[0]), normalizeConstraint(pair[1])
		if index == 0 {
			if !LanguageMatches(want, actual) {
				return "mismatch"
			}
			continue
		}
		if want != actual {
			return "mismatch"
		}
	}
	if unknown {
		return "unknown"
	}
	return "match"
}
func (s *Store) constraints(ctx context.Context, id int64) (Constraints, error) {
	var raw []byte
	var c Constraints
	e := s.db.QueryRowContext(ctx, `SELECT data FROM request_constraints WHERE request_id=?`, id).Scan(&raw)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(raw, &c)
	return c, e
}
