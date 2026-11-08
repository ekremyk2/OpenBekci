// Package codes holds the human labels of the API's enums and error codes in
// English and Turkish (GET /v1/codes). Packs add labels in 0.4.
package codes

import "strings"

// Locales are the supported label languages.
var Locales = []string{"en", "tr"}

type pair struct{ en, tr string }

// sections maps section name -> code -> labels.
var sections = map[string]map[string]pair{
	"roles": {
		"org_admin":    {"Organization administrator", "Organizasyon yöneticisi"},
		"site_manager": {"Site manager", "Site yöneticisi"},
		"board_member": {"Board member", "Yönetim kurulu üyesi"},
		"auditor":      {"Auditor", "Denetçi"},
		"accountant":   {"Accountant", "Muhasebeci"},
		"staff":        {"Staff", "Personel"}, //nolint:misspell // Turkish
		"guard":        {"Guard", "Güvenlik"},
	},
	"unit_types": {
		"flat":    {"Flat", "Daire"},
		"shop":    {"Shop", "Dükkan"},
		"office":  {"Office", "Büro"},
		"parking": {"Parking spot", "Otopark"},
		"storage": {"Storage room", "Depo"},
		"common":  {"Common area", "Ortak alan"},
		"other":   {"Other", "Diğer"},
	},
	"unit_statuses": {
		"active": {"Active", "Aktif"},
		"merged": {"Merged", "Birleştirildi"},
		"split":  {"Split", "Bölündü"},
	},
	"occupancy_kinds": {
		"owner":     {"Owner", "Malik"},
		"tenant":    {"Tenant", "Kiracı"},
		"household": {"Household member", "Hane üyesi"},
	},
	"unit_sides": {
		"owner":  {"Owner side", "Malik tarafı"},
		"tenant": {"Tenant side", "Kiracı tarafı"},
	},
	"allocation_key_kinds": {
		"share": {"Share (e.g. land share)", "Pay (ör. arsa payı)"},
		"equal": {"Equal per unit", "Daire başına eşit"},
		"area":  {"By area (m²)", "Alana göre (m²)"},
		"table": {"Amount table", "Tutar tablosu"},
	},
	"allocation_keys": {
		"arsa_payi": {"Land share (arsa payı)", "Arsa payı"},
		"equal":     {"Equal per unit", "Daire başına eşit"},
		"area":      {"By area", "Alana göre"},
	},
	"invitation_kinds": {
		"manager":   {"Invited by the manager", "Yönetici davetiyesi"},
		"household": {"Household invitation", "Hane üyesi davetiyesi"},
		"site_code": {"Site join code", "Site katılım kodu"},
	},
	"invitation_statuses": {
		"pending":  {"Pending", "Bekliyor"},
		"accepted": {"Accepted", "Kabul edildi"},
		"revoked":  {"Revoked", "İptal edildi"},
		"expired":  {"Expired", "Süresi doldu"},
	},
	"invitation_channels": {
		"sms":   {"SMS", "SMS"},
		"email": {"Email", "E-posta"},
		"link":  {"Link only", "Yalnızca bağlantı"},
	},
	"claim_statuses": {
		"pending":  {"Awaiting approval", "Onay bekliyor"},
		"approved": {"Approved", "Onaylandı"},
		"rejected": {"Rejected", "Reddedildi"},
	},
	"open_charges": {
		"carry_to_person": {"Carried to the person's own account", "Kişinin kendi hesabına aktarılır"},
		"keep_on_unit":    {"Kept on the unit", "Dairede kalır"},
		"write_off":       {"Written off", "Silinir"},
	},
	"ticket_visibility": {
		"private": {"Private (my unit and management)", "Özel (dairem ve yönetim)"},
		"common":  {"Common (whole site)", "Ortak (tüm site)"},
	},
	"ticket_status_semantics": {
		"open_like":   {"Open", "Açık"},
		"closed_like": {"Closed", "Kapalı"},
	},
	"ticket_assignee_kinds": {
		"person": {"Staff member", "Personel"}, //nolint:misspell // Turkish
		"vendor": {"Vendor", "Tedarikçi"},
	},
	"comment_visibility": {
		"public":   {"Visible to the resident", "Sakine görünür"},
		"internal": {"Internal note", "Dahili not"},
	},
	"assignee_roles": {
		"site_manager": {"Site manager", "Site yöneticisi"},
		"staff":        {"Staff", "Personel"}, //nolint:misspell // Turkish
		"board_member": {"Board member", "Yönetim kurulu üyesi"},
	},
	"announcement_statuses": {
		"draft":     {"Draft", "Taslak"},
		"scheduled": {"Scheduled", "Zamanlandı"},
		"published": {"Published", "Yayımlandı"},
		"archived":  {"Archived", "Arşivlendi"},
	},
	"announcement_audiences": {
		"site":    {"Whole site", "Tüm site"},
		"block":   {"One block", "Tek blok"},
		"owners":  {"Owners", "Malikler"},
		"tenants": {"Tenants", "Kiracılar"},
		"units":   {"Chosen units", "Seçilen daireler"},
	},
	"notification_channels": {
		"in_app": {"In the app", "Uygulama içi"},
		"email":  {"Email", "E-posta"},
		"sms":    {"SMS", "SMS"},
		"push":   {"Push", "Anlık bildirim"},
	},
	"notification_types": {
		"announcement.published":     {"Announcement", "Duyuru"},
		"charge.due_soon":            {"Payment due soon", "Ödeme tarihi yaklaşıyor"},
		"charge.overdue":             {"Payment overdue", "Ödeme gecikti"},
		"payment.receipt":            {"Payment receipt", "Ödeme makbuzu"},
		"payment.declared":           {"Payment declared", "Ödeme bildirildi"},
		"payment.confirmed":          {"Payment confirmed", "Ödeme onaylandı"},
		"payment.rejected":           {"Payment rejected", "Ödeme reddedildi"},
		"statement.sent":             {"Statement", "Ekstre"},
		"ticket.updated":             {"Ticket updates", "Talep güncellemeleri"},
		"ticket.commented":           {"Ticket comments", "Talep yorumları"},
		"claim.requested":            {"Unit claim requested", "Daire talebi"},
		"claim.decided":              {"Unit claim decided", "Daire talebi sonuçlandı"},
		"meeting.called":             {"Meeting called", "Toplantıya çağrı"},
		"meeting.decisions":          {"Meeting decisions", "Toplantı kararları"},
		"meeting.assembly_due":       {"Ordinary assembly due", "Olağan genel kurul zamanı"},
		"delivery.waiting":           {"Parcel waiting", "Kargo bekliyor"},
		"visitor.qr_rotated":         {"New visitor QR code", "Yeni ziyaretçi QR kodu"},
		"term.expiring":              {"Term ending", "Görev süresi doluyor"},
		"visitor.approval_requested": {"Visitor at the gate", "Kapıda ziyaretçi"},
		"visitor.arrived":            {"Visitor arrived", "Ziyaretçi geldi"},
		"booking.confirmed":          {"Booking confirmed", "Rezervasyon onaylandı"},
		"booking.cancelled":          {"Booking cancelled", "Rezervasyon iptal edildi"},
		"delivery.received":          {"Parcel waiting", "Kargo var"},
		"access.agent_silent":        {"Gate agent silent", "Geçiş ajanı sessiz"},
		"access.agent_clock_skew":    {"Gate agent clock skew", "Geçiş ajanı saat farkı"},
		"access.agent_stopped":       {"Gate agent stopped", "Geçiş ajanı durdu"},
		"auth.sign_in_code":          {"Sign-in code", "Giriş kodu"},
		"invitation.sent":            {"Invitation", "Davetiye"},
	},
	"document_visibility_levels": {
		"board":    {"Board only", "Yalnızca yönetim"},
		"owners":   {"Owners", "Malikler"},
		"everyone": {"Everyone", "Herkes"},
		"units":    {"Chosen units", "Seçilen daireler"},
	},
	"document_sources": {
		"uploaded":  {"Uploaded", "Yüklendi"},
		"generated": {"Generated", "Otomatik oluşturuldu"},
	},
	"folder_kinds": {
		"site": {"Site folder", "Site klasörü"},
		"unit": {"Unit folder", "Daire klasörü"},
	},
	"site_statuses": {
		"active":     {"Active", "Aktif"},
		"offboarded": {"Handed over", "Devredildi"},
	},
	"scope_types": {
		"org":   {"Organization", "Organizasyon"},
		"site":  {"Site", "Site"},
		"block": {"Block", "Blok"},
	},
	"problem_codes": {
		"validation_failed":       {"The request is not valid", "İstek geçerli değil"},
		"bad_request":             {"The request is malformed", "İstek bozuk"},
		"unauthenticated":         {"Sign in to continue", "Devam etmek için giriş yapın"},
		"not_found":               {"Not found", "Bulunamadı"},
		"conflict":                {"Conflicts with the current state", "Mevcut durumla çelişiyor"},
		"version_conflict":        {"Changed by someone else; reload and try again", "Başkası tarafından değiştirildi; yenileyip tekrar deneyin"},
		"idempotency_mismatch":    {"This key was used for a different request", "Bu anahtar başka bir istek için kullanıldı"},
		"locked_period":           {"The period is locked", "Dönem kilitli"},
		"insufficient_permission": {"You do not have permission", "Yetkiniz yok"},
		"reauth_required":         {"Confirm your identity again", "Kimliğinizi yeniden doğrulayın"},
		"invalid_grant":           {"The credentials or code are not valid", "Bilgiler veya kod geçerli değil"},
		"guard_session_required":  {"Pick your name and enter your PIN", "Adınızı seçip PIN girin"},
		"guard_session_expired":   {"Your guard session ended; enter your PIN again", "Güvenlik oturumunuz bitti; PIN'inizi yeniden girin"},
		"rate_limited":            {"Too many attempts; try again later", "Çok fazla deneme; sonra tekrar deneyin"},
		"site_offboarded":         {"This site was handed over", "Bu site devredildi"},
		"internal_error":          {"Something went wrong", "Bir şeyler ters gitti"},
		"sms_unavailable":         {"SMS is not available on this server", "Bu sunucuda SMS kullanılamıyor"},
		"mail_unavailable":        {"Email is not available on this server", "Bu sunucuda e-posta kullanılamıyor"},
		"payload_too_large":       {"The upload is too large", "Yükleme çok büyük"},
	},
	"field_error_codes": {
		"required":       {"Required", "Zorunlu"},
		"invalid":        {"Not valid", "Geçersiz"},
		"invalid_type":   {"Wrong type", "Yanlış tür"},
		"invalid_format": {"Wrong format", "Yanlış biçim"},
		"invalid_value":  {"Value not allowed", "Değer kabul edilmiyor"},
		"too_short":      {"Too short", "Çok kısa"},
		"too_long":       {"Too long", "Çok uzun"},
		"too_small":      {"Too small", "Çok küçük"},
		"too_large":      {"Too large", "Çok büyük"},
		"unknown_field":  {"Unknown field", "Bilinmeyen alan"},
		"conflict":       {"Already exists", "Zaten var"},

		"invalid_length":        {"Wrong length", "Uzunluk uygun değil"},
		"duplicate":             {"Given twice", "İki kez verilmiş"},
		"too_many":              {"Too many", "Çok fazla"},
		"not_allowed":           {"Not allowed here", "Burada izin verilmiyor"},
		"not_mutable":           {"Cannot be muted", "Sessize alınamaz"},
		"locked":                {"Cannot be changed", "Değiştirilemez"},
		"unexpected":            {"Not expected here", "Burada beklenmiyor"},
		"needs_open_and_closed": {"Needs an open-like and a closed-like status", "Açık ve kapalı türünde birer durum gerekli"},
	},
	"import_codes": {
		"share_sum_mismatch":    {"The shares do not add up to one", "Paylar toplamı bire eşit değil"},
		"conflicting_unit_data": {"The file gives different data for the same door number", "Dosyada aynı kapı numarası için farklı veri var"},
		"no_tenant_in_row":      {"A tenant balance is given but the row has no tenant", "Kiracı bakiyesi var ama satırda kiracı yok"},
	},
}

// Normalize returns a supported locale for a request value such as "tr-TR".
func Normalize(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	for _, s := range Locales {
		if l == s {
			return l
		}
	}
	return "en"
}

// Labels returns every section's labels in the locale (normalized).
func Labels(locale string) (string, map[string]map[string]string) {
	locale = Normalize(locale)
	out := make(map[string]map[string]string, len(sections))
	for name, codes := range sections {
		m := make(map[string]string, len(codes))
		for code, p := range codes {
			if locale == "tr" {
				m[code] = p.tr
			} else {
				m[code] = p.en
			}
		}
		out[name] = m
	}
	return locale, out
}

// Sections lists the section names.
func Sections() []string {
	out := make([]string, 0, len(sections))
	for k := range sections {
		out = append(out, k)
	}
	return out
}
