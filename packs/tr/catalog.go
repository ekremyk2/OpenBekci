// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import "strings"

// labels is the catalog of everything the pack adds: template names, preset
// names, validator codes, address field labels and consent names. Keys are
// "tr.<key>"; the core merges them into the codes endpoint for organizations
// that enabled the pack.
type labels struct{}

func (labels) ID() string { return ID + ".labels" }

var labelsTR = map[string]string{
	"tr.call_letter":         "Toplantı çağrısı",
	"tr.attendance_list":     "Hazirun cetveli",
	"tr.minutes":             "Toplantı tutanağı",
	"tr.delivery_checklist":  "Çağrı tebliğ listesi",
	"tr.proxy_form":          "Vekâletname",
	"tr.decision_book_draft": "Karar defteri sayfa taslağı",
	"tr.receipt":             "Aidat makbuzu",
	"tr.statement":           "Hesap ekstresi",
	"tr.closing_statement":   "Kapanış hesap özeti",
	"tr.debt_certificate":    "Borç durum belgesi",
	"tr.demand_letter":       "İhtarname",

	"tr.preset.quorum":      "Toplantı yeter sayısı (634 sayılı Kanun)",
	"tr.preset.thresholds":  "Karar yeter sayıları (634 sayılı Kanun)",
	"tr.preset.vote_weight": "Oy ve vekâlet kuralları (634 sayılı Kanun)",
	"tr.preset.schedule":    "Toplantı takvimi (634 sayılı Kanun)",
	"tr.preset.transfer":    "Devir ve taşınma: borç kişide kalır",
	"tr.preset.late_fee":    "Gecikme tazminatı (aylık yüzde 5 tavan)",

	CodeTaxIDFormat:   "Vergi numarası 10 haneli (TCKN 11 haneli) olmalıdır",
	CodeTaxIDChecksum: "Vergi numarası veya TCKN doğrulama hanesi hatalı",
	CodeIBANFormat:    "IBAN TR ile başlayan 26 karakter olmalıdır",
	CodeIBANChecksum:  "IBAN kontrol hanesi hatalı",
	CodeIBANReserved:  "IBAN'ın 10. karakteri (yedek hane) 0 olmalıdır",
	CodeIBANBank:      "IBAN banka kodu geçersiz",
	CodePlateFormat:   "Plaka biçimi geçersiz (örnek: 34 ABC 123)",
	CodePlateProvince: "Plakadaki il kodu 01 ile 81 arasında olmalıdır",

	"tr.address.line1":       "Mahalle, cadde, sokak, kapı no",
	"tr.address.line2":       "Adres satırı 2",
	"tr.address.province":    "İl",
	"tr.address.district":    "İlçe",
	"tr.address.postal_code": "Posta kodu",
	"tr.address.uavt":        "UAVT adres kodu",

	"tr.privacy_notice":    "KVKK aydınlatma metni",
	"tr.consent_identity":  "Kimlik bilgileri için açık rıza",
	"tr.consent_contact":   "İletişim bilgileri için açık rıza",
	"tr.consent_financial": "Mali bilgiler için açık rıza",
	"tr.consent_camera":    "Kamera ve ziyaretçi kayıtları için açık rıza",

	"tr.settings.kvkk_data_controller":     "KVKK veri sorumlusu",
	"tr.settings.iyzico":                   "iyzico ile kartla ödeme",
	"tr.settings.iletimerkezi":             "İleti Merkezi SMS",
	"tr.settings.bank_formats":             "Banka ekstre biçimleri",
	"tr.settings.iyzico_callback_base_url": "Sunucunun herkese açık adresi (iyzico geri çağrısı için)",
	"tr.iyzico":                            "iyzico (kart)",
	"tr.iletimerkezi":                      "İleti Merkezi SMS",
	"tr.ziraat":                            "Ziraat Bankası ekstresi",
	"tr.isbank":                            "İş Bankası ekstresi",
}

var labelsEN = map[string]string{
	"tr.call_letter":         "Assembly call letter",
	"tr.attendance_list":     "Attendance list (hazirun cetveli)",
	"tr.minutes":             "Meeting minutes",
	"tr.delivery_checklist":  "Call delivery checklist",
	"tr.proxy_form":          "Proxy form (vekaletname)",
	"tr.decision_book_draft": "Decision book page draft",
	"tr.receipt":             "Dues receipt (makbuz)",
	"tr.statement":           "Account statement",
	"tr.closing_statement":   "Closing statement",
	"tr.debt_certificate":    "Debt status certificate",
	"tr.demand_letter":       "Demand letter (ihtarname)",

	"tr.preset.quorum":      "Quorum (Law 634)",
	"tr.preset.thresholds":  "Decision thresholds (Law 634)",
	"tr.preset.vote_weight": "Votes and proxies (Law 634)",
	"tr.preset.schedule":    "Meeting schedule (Law 634)",
	"tr.preset.transfer":    "Move-out: debt stays with the person",
	"tr.preset.late_fee":    "Late fee (5 % monthly ceiling)",

	CodeTaxIDFormat:   "A tax number has 10 digits (a TCKN has 11)",
	CodeTaxIDChecksum: "The tax number or TCKN fails its check digit",
	CodeIBANFormat:    "A Turkish IBAN is 26 characters starting with TR",
	CodeIBANChecksum:  "The IBAN check digits are wrong",
	CodeIBANReserved:  "The 10th character (reserved digit) of the IBAN must be 0",
	CodeIBANBank:      "The IBAN bank code is not valid",
	CodePlateFormat:   "The plate format is not valid (for example 34 ABC 123)",
	CodePlateProvince: "The province code on the plate must be 01 to 81",

	"tr.address.line1":       "Neighbourhood, street, door number",
	"tr.address.line2":       "Address line 2",
	"tr.address.province":    "Province (il)",
	"tr.address.district":    "District (ilçe)",
	"tr.address.postal_code": "Postal code",
	"tr.address.uavt":        "UAVT address code",

	"tr.privacy_notice":    "KVKK privacy notice",
	"tr.consent_identity":  "Explicit consent: identity data",
	"tr.consent_contact":   "Explicit consent: contact data",
	"tr.consent_financial": "Explicit consent: financial data",
	"tr.consent_camera":    "Explicit consent: camera and visitor logs",

	"tr.settings.kvkk_data_controller":     "KVKK data controller",
	"tr.settings.iyzico":                   "Card payments with iyzico",
	"tr.settings.iletimerkezi":             "İleti Merkezi SMS",
	"tr.settings.bank_formats":             "Bank statement formats",
	"tr.settings.iyzico_callback_base_url": "Public address of the server (for the iyzico callback)",
	"tr.iyzico":                            "iyzico (card)",
	"tr.iletimerkezi":                      "İleti Merkezi SMS",
	"tr.ziraat":                            "Ziraat Bankası statement",
	"tr.isbank":                            "İş Bankası statement",
}

// Labels implements sdk.Catalog. Turkish and English are supplied; the host
// asks for the locale and its fallbacks.
func (labels) Labels(locale string) map[string]string {
	var src map[string]string
	switch {
	case strings.HasPrefix(strings.ToLower(locale), "tr"):
		src = labelsTR
	case strings.HasPrefix(strings.ToLower(locale), "en"), locale == "":
		src = labelsEN
	default:
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
