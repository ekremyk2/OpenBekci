package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/textproto"
	"testing"
)

// The web wizard (FormData) sends `mapping` as a plain part without a
// Content-Type; scripted clients send application/json. Both must work.
func TestIntegrationImportMappingPartContentTypes(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	imp := func(ct, mapping string) resp {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		_ = mw.WriteField("site_id", f.Site.String())
		if mapping != "" {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="mapping"`)
			if ct != "" {
				h.Set("Content-Type", ct)
			}
			w, _ := mw.CreatePart(h)
			_, _ = w.Write([]byte(mapping))
		}
		fw, _ := mw.CreateFormFile("file", "f.csv")
		_, _ = fw.Write([]byte("Daire,Sahip\nM1,Ayse Kaya\n"))
		_ = mw.Close()
		return p.call("POST", p.base()+"/units:import?dry_run=true", b.String(), p.adm, "Content-Type", mw.FormDataContentType())
	}
	const m = `{"columns":{"door_number":"Daire","owner_name":"Sahip"}}`
	p.must(imp("", ""), 200)
	p.must(imp("application/json", m), 200)
	p.must(imp("", m), 200)
	p.must(imp("text/plain", m), 200)
}
