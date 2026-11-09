// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// adminAreasJSON is the il / ilçe list: 81 provinces (code = plate code) and
// their 973 districts, a static versioned data file (about 13 KB). Mahalle
// (neighbourhood) lists are not included: the public datasets are about 11 MB,
// far above the 2 MB the pack may embed, so the address form stops at ilçe.
//
//go:embed data/admin_areas.json
var adminAreasJSON []byte

type adminAreas struct {
	Version   string              `json:"version"`
	Provinces [][2]string         `json:"provinces"`
	Districts map[string][]string `json:"districts"`
}

var areaData = mustAreas()

func mustAreas() adminAreas {
	var a adminAreas
	if err := json.Unmarshal(adminAreasJSON, &a); err != nil {
		panic(fmt.Sprintf("tr: data/admin_areas.json: %v", err))
	}
	return a
}

// DataVersion is the version of the embedded il / ilçe list.
func DataVersion() string { return areaData.Version }

// areas serves il, ilçe, public holidays and the Turkish address format
// through the core's reference-data endpoint.
type areas struct{}

func (areas) ID() string { return ID + ".areas" }

func (areas) Kinds() []string { return []string{"province", "district", "holiday"} }

// districtCode is the code of a district: the province code, a colon and the
// ascii-folded lower-case name ("34:kadikoy").
func districtCode(province, name string) string { return province + ":" + fold(name) }

func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'İ', 'I', 'ı', 'i':
			b.WriteByte('i')
		case 'Ğ', 'ğ':
			b.WriteByte('g')
		case 'Ü', 'ü':
			b.WriteByte('u')
		case 'Ş', 'ş':
			b.WriteByte('s')
		case 'Ö', 'ö':
			b.WriteByte('o')
		case 'Ç', 'ç':
			b.WriteByte('c')
		case 'Â', 'â':
			b.WriteByte('a')
		case 'Î', 'î':
			b.WriteByte('i')
		case 'Û', 'û':
			b.WriteByte('u')
		default:
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
			case r >= 'A' && r <= 'Z':
				b.WriteRune(r + 32)
			case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// List implements sdk.ReferenceData. province has no parent; district takes a
// province code as parent (all districts when empty); holiday takes a year as
// parent (all years when empty).
func (areas) List(_ context.Context, kind, parent string) ([]sdk.ReferenceItem, error) {
	switch kind {
	case "province":
		out := make([]sdk.ReferenceItem, 0, len(areaData.Provinces))
		for _, p := range areaData.Provinces {
			out = append(out, sdk.ReferenceItem{Code: p[0], Name: p[1]})
		}
		return out, nil
	case "district":
		var codes []string
		if parent != "" {
			codes = []string{parent}
		} else {
			for _, p := range areaData.Provinces {
				codes = append(codes, p[0])
			}
		}
		var out []sdk.ReferenceItem
		for _, pc := range codes {
			for _, d := range areaData.Districts[pc] {
				out = append(out, sdk.ReferenceItem{Code: districtCode(pc, d), Name: d, Parent: pc})
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return collate(out[i].Name) < collate(out[j].Name) })
		return out, nil
	case "holiday":
		var out []sdk.ReferenceItem
		for _, h := range Holidays() {
			if parent == "" || strings.HasPrefix(h.Date, parent) {
				name := h.Name
				if h.HalfDay {
					name += " (yarım gün)"
				}
				out = append(out, sdk.ReferenceItem{Code: h.Date, Name: name, Parent: h.Date[:4]})
			}
		}
		return out, nil
	}
	return nil, nil
}

// collate gives a sort key that puts Turkish letters where a Turkish reader
// expects them (c, ç, d ...; ı before i).
func collate(s string) string {
	order := map[rune]rune{'ç': 'c' + 1, 'ğ': 'g' + 1, 'ı': 'h', 'ö': 'o' + 1, 'ş': 's' + 1, 'ü': 'u' + 1, 'â': 'a', 'î': 'i', 'û': 'u'}
	var b strings.Builder
	for _, r := range turkishLower(s) {
		if m, ok := order[r]; ok {
			r = m
		}
		b.WriteRune(r)
	}
	return b.String()
}

func turkishLower(s string) string {
	s = strings.NewReplacer("İ", "i", "I", "ı").Replace(s)
	return strings.ToLower(s)
}

// Holiday is a public holiday.
type Holiday struct {
	// Date is YYYY-MM-DD.
	Date string
	Name string
	// HalfDay marks an arife (the half day before a religious holiday) or
	// 28 October.
	HalfDay bool
}

// Holidays returns the Turkish public holidays of 2025 to 2027: the fixed
// national holidays and the religious holidays (Ramazan and Kurban Bayramı) as
// announced by the Diyanet calendar. Religious dates follow the lunar
// calendar and are confirmed each year by the Diyanet and the Official
// Gazette: check them before relying on a date (LEGAL.md, "verify").
func Holidays() []Holiday {
	var out []Holiday
	for year := 2025; year <= 2027; year++ {
		y := fmt.Sprint(year)
		out = append(out,
			Holiday{y + "-01-01", "Yılbaşı", false},
			Holiday{y + "-04-23", "Ulusal Egemenlik ve Çocuk Bayramı", false},
			Holiday{y + "-05-01", "Emek ve Dayanışma Günü", false},
			Holiday{y + "-05-19", "Atatürk'ü Anma, Gençlik ve Spor Bayramı", false},
			Holiday{y + "-07-15", "Demokrasi ve Millî Birlik Günü", false},
			Holiday{y + "-08-30", "Zafer Bayramı", false},
			Holiday{y + "-10-28", "Cumhuriyet Bayramı arifesi", true},
			Holiday{y + "-10-29", "Cumhuriyet Bayramı", false},
		)
	}
	out = append(out, religious...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// religious holds the Ramazan Bayramı (3 days) and Kurban Bayramı (4 days)
// with their arife days.
var religious = func() []Holiday {
	type span struct {
		arife string
		first string
		days  int
		name  string
	}
	spans := []span{
		{"2025-03-29", "2025-03-30", 3, "Ramazan Bayramı"}, {"2025-06-05", "2025-06-06", 4, "Kurban Bayramı"},
		{"2026-03-19", "2026-03-20", 3, "Ramazan Bayramı"}, {"2026-05-26", "2026-05-27", 4, "Kurban Bayramı"},
		{"2027-03-08", "2027-03-09", 3, "Ramazan Bayramı"}, {"2027-05-15", "2027-05-16", 4, "Kurban Bayramı"},
	}
	var out []Holiday
	for _, s := range spans {
		out = append(out, Holiday{s.arife, s.name + " arifesi", true})
		y, m, d := 0, 0, 0
		_, _ = fmt.Sscanf(s.first, "%d-%d-%d", &y, &m, &d)
		for i := 0; i < s.days; i++ {
			out = append(out, Holiday{addDays(y, m, d, i), fmt.Sprintf("%s %d. gün", s.name, i+1), false})
		}
	}
	return out
}()

func addDays(y, m, d, n int) string {
	days := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if y%4 == 0 {
		days[1] = 29
	}
	d += n
	for d > days[m-1] {
		d -= days[m-1]
		m++
		if m > 12 {
			m = 1
			y++
		}
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}
