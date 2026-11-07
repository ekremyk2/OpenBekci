package app

import "testing"

func TestLoadConfigInstanceSettings(t *testing.T) {
	c, err := LoadConfig("", env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.InstanceName != "OpenBekci" || c.MinAppVersionAndroid != "1.0.0" || c.MinAppVersionIOS != "1.0.0" || c.ValidateResponses {
		t.Errorf("defaults wrong: %+v", c)
	}

	e := baseEnv()
	e["OPENBEKCI_INSTANCE_NAME"] = "Gunes Sitesi"
	e["OPENBEKCI_MIN_APP_VERSION_ANDROID"] = "1.2.0"
	e["OPENBEKCI_MIN_APP_VERSION_IOS"] = "1.3.0"
	e["OPENBEKCI_BRANDING_LOGO_URL"] = "https://x.example/logo.png"
	e["OPENBEKCI_BRANDING_PRIMARY_COLOR"] = "#112233"
	e["OPENBEKCI_API_VALIDATE_RESPONSES"] = "true"
	c, err = LoadConfig("", env(e))
	if err != nil {
		t.Fatal(err)
	}
	if c.InstanceName != "Gunes Sitesi" || c.MinAppVersionAndroid != "1.2.0" || c.MinAppVersionIOS != "1.3.0" ||
		c.BrandingLogoURL != "https://x.example/logo.png" || c.BrandingPrimaryColor != "#112233" || !c.ValidateResponses {
		t.Errorf("settings wrong: %+v", c)
	}
}

func TestLoadConfigMaxBodyBytes(t *testing.T) {
	c, err := LoadConfig("", env(baseEnv()))
	if err != nil || c.MaxBodyBytes != 0 {
		t.Fatalf("default: %v %d", err, c.MaxBodyBytes)
	}
	e := baseEnv()
	e["OPENBEKCI_MAX_BODY_BYTES"] = "2048"
	if c, err = LoadConfig("", env(e)); err != nil || c.MaxBodyBytes != 2048 {
		t.Fatalf("set: %v %d", err, c.MaxBodyBytes)
	}
	for _, bad := range []string{"0", "-1", "lots"} {
		e["OPENBEKCI_MAX_BODY_BYTES"] = bad
		if _, err := LoadConfig("", env(e)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
