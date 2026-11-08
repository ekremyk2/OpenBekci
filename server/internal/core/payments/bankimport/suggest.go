package bankimport

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Reference scheme: "<door>-<period>", for example "A-12-2026-03". The door
// number may itself contain hyphens; the period is the trailing YYYY-MM.
var refRe = regexp.MustCompile(`^(.*?)[\s/-]*(\d{4})[-/.](0[1-9]|1[0-2])\s*$`)

// BuildReference returns the reference a resident writes on the transfer.
func BuildReference(door, period string) string { return door + "-" + period }

// ParseReference splits a reference or description at its trailing period
// (YYYY-MM). door is the text before the period (it may carry other words, so
// callers compare it on a token boundary with ReferenceHasDoor); ok is false
// when the text does not end in a period.
func ParseReference(s string) (door, period string, ok bool) {
	s = strings.TrimSpace(s)
	m := refRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return strings.TrimSpace(m[1]), m[2] + "-" + m[3], true
}

// ReferenceHasDoor reports whether the normalized prefix ends with the door number on a token boundary.
func ReferenceHasDoor(prefix, door string) bool {
	p, d := " "+Normalize(prefix), Normalize(door)
	return d != "" && strings.HasSuffix(p, " "+d)
}

// Normalize folds a name for comparison: lower case with Turkish letters
// mapped (ı and İ to i), accents and punctuation removed, spaces collapsed.
func Normalize(s string) string {
	s = strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "ş", "s", "Ş", "s", "ğ", "g", "Ğ", "g", "ü", "u", "Ü", "u", "ö", "o", "Ö", "o", "ç", "c", "Ç", "c").Replace(s)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	if out, _, err := transform.String(t, s); err == nil {
		s = out
	}
	s = strings.ToLower(s)
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Candidate is one unit side a line could belong to.
type Candidate struct {
	UnitID     uuid.UUID
	Door       string
	Side       string
	PersonID   *uuid.UUID
	PersonName string
	// OpenTotal is the side's open principal plus fees; OpenCharges are the single open charge amounts; Periods the periods of the open charges.
	OpenTotal   int64
	OpenCharges []int64
	Periods     []string
}

// Suggestion is a ranked proposal.
type Suggestion struct {
	UnitID         uuid.UUID  `json:"unit_id"`
	UnitDoorNumber string     `json:"unit_door_number"`
	DebtorKind     string     `json:"debtor_kind"`
	PersonID       *uuid.UUID `json:"person_id"`
	Score          float64    `json:"score"`
	Reason         string     `json:"reason"`
}

// Scores of the signals; they add up to at most 1.0.
const (
	scoreReference = 0.55
	scorePeriod    = 0.15
	scoreName      = 0.30
	scoreAmountAll = 0.20
	scoreAmountOne = 0.12
	minScore       = 0.30
	maxSuggestions = 3
)

func nameSimilarity(counterparty string, personName string) float64 {
	a, b := strings.Fields(Normalize(counterparty)), strings.Fields(Normalize(personName))
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	hit := 0
	for _, t := range b {
		if set[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(b))
}

// Rank scores the candidates for an incoming line (positive amount) and returns
// at most three suggestions with a score of at least 0.3, best first. Signals:
// the reference scheme (door, plus the period of an open charge), the payer
// name against the side's person, and the amount against the side's open
// total or one open charge. Ties favor the side with a matching name, then owner.
func Rank(counterparty, description, reference string, amount int64, cands []Candidate) []Suggestion {
	if amount <= 0 {
		return nil
	}
	door, period, haveRef := ParseReference(reference)
	if !haveRef {
		door, period, haveRef = ParseReference(description)
	}
	var out []Suggestion
	for _, c := range cands {
		var score float64
		var reasons []string
		if haveRef && ReferenceHasDoor(door, c.Door) {
			score += scoreReference
			reasons = append(reasons, "reference")
			for _, p := range c.Periods {
				if p == period {
					score += scorePeriod
					reasons = append(reasons, "period")
					break
				}
			}
		}
		if sim := nameSimilarity(counterparty, c.PersonName); sim > 0 {
			score += scoreName * sim
			reasons = append(reasons, "payer_name")
		}
		if c.OpenTotal > 0 && amount == c.OpenTotal {
			score += scoreAmountAll
			reasons = append(reasons, "amount")
		} else {
			for _, a := range c.OpenCharges {
				if a == amount {
					score += scoreAmountOne
					reasons = append(reasons, "amount")
					break
				}
			}
		}
		if score < minScore {
			continue
		}
		out = append(out, Suggestion{UnitID: c.UnitID, UnitDoorNumber: c.Door, DebtorKind: c.Side, PersonID: c.PersonID, Score: round2(score), Reason: strings.Join(reasons, ",")})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].DebtorKind == "owner" && out[j].DebtorKind != "owner"
	})
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
