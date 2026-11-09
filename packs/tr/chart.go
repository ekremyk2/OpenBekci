// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import "strings"

// chart localizes the names of the generic chart of accounts. Only names:
// the structure (codes, types, subtypes, funds) is the core's. Where useful the
// wording follows the Tek Düzen Hesap Planı (the Turkish uniform chart of
// accounts); the comment on each line gives the Tek Düzen account it echoes.
type chart struct{}

func (chart) ID() string { return ID }

// chartNamesTR maps account codes of the generic chart to Turkish names.
var chartNamesTR = map[string]string{
	"1100": "Bankalar",                                // 102 Bankalar
	"1200": "Alacaklar: kat malikleri",                // 120 Alıcılar
	"1210": "Alacaklar: kiracılar",                    // 120 Alıcılar
	"1220": "Alacaklar: kişiler (devralınan borç)",    // 136 Diğer çeşitli alacaklar
	"1300": "Peşin ödenen vergiler (stopaj alacağı)",  // 193 Peşin ödenen vergiler ve fonlar
	"2000": "Alınan avanslar (fazla ödemeler)",        // 340 Alınan sipariş avansları
	"2100": "Alınan depozito ve teminatlar",           // 326 Alınan depozito ve teminatlar
	"2200": "Satıcılar",                               // 320 Satıcılar
	"2300": "Kıdem tazminatı karşılığı",               // 472 Kıdem tazminatı karşılığı
	"3000": "Açılış bakiyesi (özkaynak)",              // 500 Sermaye
	"3100": "Geçmiş yıllar fazlası",                   // 570 Geçmiş yıllar karları
	"4000": "Aidat gelirleri",                         // 600 Yurt içi satışlar
	"4010": "Yedek akçe katkıları",                    //
	"4020": "Ek aidat (özel tahakkuk) gelirleri",      //
	"4030": "Tüketim yansıtma gelirleri",              // 649 Diğer olağan gelir ve karlar
	"4040": "Rezervasyon gelirleri",                   //
	"4050": "Hukuki masraf iadeleri",                  //
	"4100": "Gecikme tazminatı gelirleri",             // 642 Faiz gelirleri
	"4900": "Diğer gelirler",                          // 649
	"5000": "Genel yönetim giderleri",                 // 770 Genel yönetim giderleri
	"5010": "Elektrik, su ve yakıt giderleri",         // 770
	"5020": "Bakım ve onarım giderleri",               // 770
	"5030": "Personel giderleri (kapıcı, görevliler)", // 770
	"5040": "Sigorta giderleri",                       // 770
	"5050": "Ödeme sağlayıcı komisyon giderleri",      // 653 Komisyon giderleri
	"5900": "Diğer giderler",                          // 689 Diğer olağandışı gider ve zararlar
}

// Names implements sdk.ChartOfAccounts. Only Turkish names are supplied: the
// core's own names are English.
func (chart) Names(locale string) map[string]string {
	if !strings.HasPrefix(strings.ToLower(locale), "tr") {
		return nil
	}
	out := make(map[string]string, len(chartNamesTR))
	for k, v := range chartNamesTR {
		out[k] = v
	}
	return out
}
