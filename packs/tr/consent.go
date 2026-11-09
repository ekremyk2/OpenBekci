// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// ConsentVersion is the version of the KVKK texts. Clients record acceptance
// of a version; change it with every change of wording.
const ConsentVersion = "2026-11.1"

// consent is one KVKK text in the locales the pack has.
type consent struct {
	name string
	text map[string]string // locale to Markdown
}

func (c consent) ID() string { return ID + "." + c.name }

// Text implements sdk.ConsentTexts: Turkish and English.
func (c consent) Text(locale string) (sdk.ConsentText, bool) {
	loc := strings.ToLower(locale)
	switch {
	case strings.HasPrefix(loc, "tr"):
		loc = "tr"
	case strings.HasPrefix(loc, "en"):
		loc = "en"
	}
	md, ok := c.text[loc]
	if !ok {
		return sdk.ConsentText{}, false
	}
	return sdk.ConsentText{Version: ConsentVersion, Markdown: md}, true
}

// consents are the KVKK aydınlatma metni and the açık rıza texts per
// processing purpose: identity, contact, financial, camera and visitor logs.
// The data controller's details (legal name, address, contact, VERBİS number)
// come from the site setting tr_kvkk_data_controller; the texts name the
// fields the site fills in. The texts are templates for the site's own
// legal review, not legal advice (LEGAL.md).
func consents() []sdk.ConsentTexts {
	return []sdk.ConsentTexts{
		consent{"privacy_notice", map[string]string{"tr": noticeTR, "en": noticeEN}},
		consent{"consent_identity", map[string]string{"tr": identityTR, "en": identityEN}},
		consent{"consent_contact", map[string]string{"tr": contactTR, "en": contactEN}},
		consent{"consent_financial", map[string]string{"tr": financialTR, "en": financialEN}},
		consent{"consent_camera", map[string]string{"tr": cameraTR, "en": cameraEN}},
	}
}

const noticeTR = `# Kişisel Verilerin Korunması Hakkında Aydınlatma Metni

6698 sayılı Kişisel Verilerin Korunması Kanunu'nun (KVKK) 10. maddesi uyarınca, veri sorumlusu sıfatıyla site yönetimi olarak kişisel verilerinizin nasıl işlendiğini bildiririz.

## Veri sorumlusu

Ad / unvan, adres, iletişim ve VERBİS sicil numarası, site yönetiminin OpenBekci ayarlarındaki "KVKK veri sorumlusu" alanında yer alır.

## İşlenen veriler ve amaçlar

- **Kimlik:** ad soyad, T.C. kimlik numarası (isteğe bağlı, şifreli saklanır, yalnızca yöneticiler görür); kat maliki ve kiracı kayıtlarının tutulması, tebligat ve yasal takip.
- **İletişim:** telefon, e-posta, adres; çağrı, duyuru, bildirim ve doğrulama kodlarının gönderilmesi.
- **Mali:** aidat, borç, ödeme ve makbuz kayıtları; aidat tahsilatı, hesap ekstresi ve borç durum belgesi düzenlenmesi.
- **Kamera ve ziyaretçi kayıtları:** giriş-çıkış, plaka ve ziyaretçi kayıtları; site güvenliğinin sağlanması.
- **Toplantı kayıtları:** hazirun, vekâletname, oy ve karar kayıtları; 634 sayılı Kat Mülkiyeti Kanunu'ndan doğan yükümlülüklerin yerine getirilmesi.

## Hukuki sebep

Verileriniz KVKK m. 5/2'deki şartlara dayanılarak işlenir: kanunlarda açıkça öngörülmesi (634 sayılı Kanun, vergi ve usul mevzuatı), bir sözleşmenin ifası, hukuki yükümlülüğün yerine getirilmesi, bir hakkın tesisi, kullanılması veya korunması ve meşru menfaat. Bu sebeplerin kapsamadığı işlemler için ayrıca açık rızanız istenir; rıza vermemeniz hizmetin sunulmasını etkilemez.

## Aktarım

Verileriniz yasal zorunluluk halinde yetkili kamu kurumlarına, hukuki takip için avukat ve icra dairelerine, ödeme ve SMS hizmeti için sözleşmeli hizmet sağlayıcılara aktarılabilir. Yurt dışına aktarım KVKK m. 9 hükümlerine uygun yapılır. Borçlu listeleri hiçbir şekilde herkese açık paylaşılmaz.

## Toplama yöntemi ve saklama

Verileriniz formlar, uygulama, e-posta/SMS ve toplantı tutanakları aracılığıyla elektronik ortamda toplanır. İlgili mevzuattaki süreler ile işleme amacının gerektirdiği süre boyunca saklanır, sonra silinir veya anonimleştirilir.

## Haklarınız (KVKK m. 11)

Verilerinizin işlenip işlenmediğini öğrenme, bilgi talep etme, amacına uygun kullanılıp kullanılmadığını öğrenme, aktarıldığı kişileri bilme, eksik veya yanlış işlenmişse düzeltilmesini, silinmesini veya yok edilmesini isteme, işlemeye itiraz etme ve zarar halinde tazminat talep etme haklarına sahipsiniz. Başvurularınızı yukarıdaki iletişim adresine yazılı olarak veya uygulama üzerinden iletebilirsiniz.
`

const noticeEN = `# Privacy notice (KVKK, Law No. 6698)

Under article 10 of the Turkish Personal Data Protection Law (KVKK) the site management, as data controller, tells you how your personal data is processed.

## Data controller

Name, address, contact and VERBİS registration number are in the site management's "KVKK data controller" setting.

## Data and purposes

- **Identity:** name, national id (optional, stored encrypted, visible to managers only); keeping owner and tenant records, formal notices and legal follow-up.
- **Contact:** phone, email, address; sending calls to meetings, announcements, notifications and verification codes.
- **Financial:** dues, debts, payments and receipts; collecting dues, statements and debt status certificates.
- **Camera and visitor logs:** entry and exit, plate and visitor records; site security.
- **Meeting records:** attendance, proxies, votes and decisions; duties under the Condominium Law No. 634.

## Legal basis

Processing rests on KVKK art. 5/2: the law expressly provides for it (Law 634, tax and procedure law), performance of a contract, a legal obligation, establishing or defending a right, and legitimate interest. For anything these do not cover your explicit consent is asked separately; declining does not affect the service.

## Transfers

Data may be passed to competent public bodies where required, to lawyers and enforcement offices for legal follow-up, and to contracted payment and SMS providers. Transfers abroad follow KVKK art. 9. Lists of debtors are never shared publicly.

## Collection and retention

Data is collected electronically through forms, the app, email/SMS and meeting records, kept for the periods the law requires or the purpose needs, then deleted or anonymized.

## Your rights (KVKK art. 11)

You may ask whether your data is processed, request information, learn the purpose and whether it is used accordingly, know the recipients, have incomplete or wrong data corrected, deleted or destroyed, object to processing, and claim compensation for damage. Write to the contact above or use the app.
`

const identityTR = `# Açık rıza: kimlik bilgileri

Ad soyad ve (varsa) T.C. kimlik numaramın, kat maliki/kiracı kaydımın tutulması, tebligat ve gerekirse yasal takip amacıyla site yönetimi tarafından işlenmesine, T.C. kimlik numaramın şifreli saklanıp yalnızca yöneticilerce görülmesine açık rıza veriyorum.

Rızamı istediğim zaman geri alabileceğimi, bunun geri alma öncesi işlemleri etkilemeyeceğini biliyorum. Ayrıntılar aydınlatma metnindedir.
`

const identityEN = `# Explicit consent: identity data

I consent to the site management processing my name and (if given) my national id to keep my owner or tenant record, to serve formal notices and, where needed, for legal follow-up, and to my national id being stored encrypted and shown to managers only.

I know I can withdraw consent at any time and that this does not affect processing before withdrawal. Details are in the privacy notice.
`

const contactTR = `# Açık rıza: iletişim bilgileri

Telefon numaramın, e-posta adresimin ve adresimin; toplantı çağrıları, duyurular, bildirimler, doğrulama kodları ve makbuzların SMS, e-posta veya uygulama bildirimi ile gönderilmesi amacıyla site yönetimi ve sözleşmeli SMS/e-posta hizmet sağlayıcıları tarafından işlenmesine açık rıza veriyorum.

Gönderilen iletiler site işleyişine ilişkindir; ticari ileti değildir. Rızamı istediğim zaman geri alabilirim.
`

const contactEN = `# Explicit consent: contact data

I consent to the site management and its contracted SMS and email providers processing my phone number, email address and address to send meeting calls, announcements, notifications, verification codes and receipts by SMS, email or app notification.

Messages concern the running of the site and are not commercial messages. I can withdraw consent at any time.
`

const financialTR = `# Açık rıza: mali bilgiler

Aidat, borç, ödeme ve makbuz kayıtlarımın; tahsilat, hesap ekstresi, borç durum belgesi ve ihtarname düzenlenmesi ile bunların bana ve gerekirse yetkili kişilere iletilmesi amacıyla işlenmesine, ödeme hizmeti için sözleşmeli ödeme kuruluşuyla paylaşılmasına açık rıza veriyorum.

Borç bilgilerimin başka sakinlere veya herkese açık gösterilmeyeceğini biliyorum. Rızamı istediğim zaman geri alabilirim.
`

const financialEN = `# Explicit consent: financial data

I consent to my dues, debt, payment and receipt records being processed to collect payments, issue statements, debt status certificates and demand letters and to send them to me and, where needed, to authorised people, and to their being shared with a contracted payment provider for card payments.

I know my debt information is not shown to other residents or publicly. I can withdraw consent at any time.
`

const cameraTR = `# Açık rıza: kamera ve ziyaretçi kayıtları

Site güvenliğinin sağlanması amacıyla giriş-çıkış, plaka, ziyaretçi ve (varsa) kamera kayıtlarımın işlenmesine ve belirlenen saklama süresi boyunca tutulmasına açık rıza veriyorum.

Kayıtlara yalnızca yetkili görevliler erişir; kamera görüntüleri ve ziyaretçi kayıtları yetkisiz kişilerle paylaşılmaz. Rızamı istediğim zaman geri alabilirim; bu halde bile yasal zorunluluk veya olay incelemesi için gerekli kayıtlar mevzuata uygun saklanabilir.
`

const cameraEN = `# Explicit consent: camera and visitor logs

For site security I consent to my entry and exit, plate, visitor and (where present) camera records being processed and kept for the stated retention period.

Only authorised staff can access the records; camera footage and visitor logs are not shared with unauthorised people. I can withdraw consent at any time; records the law requires or an incident review needs may still be kept as the law allows.
`
