package pii

func init() {
	const why = "names a message template (a settings key such as reminder_1), not personal data"
	Allowlist["collections.Stage.TemplateKey"] = why
	Allowlist["collections.StageInput.TemplateKey"] = why
	Allowlist["collections.StageUpdate.TemplateKey"] = why
}
