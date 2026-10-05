package config

import "testing"

func TestSetDefaultConfig_AuthURL(t *testing.T) {
	for _, tc := range []struct{ region, authURL, want string }{
		{region: "ap-southeast-4", want: "https://iam.ap-southeast-4.myhuaweicloud.com:443/v3/"},
		{region: "", want: "https://iam.myhuaweicloud.com:443/v3/"},
		{region: "ap-southeast-4", authURL: "https://iam.example:443/v3/", want: "https://iam.example:443/v3/"},
	} {
		cc := &CloudCredentials{}
		cc.Global.Region, cc.Global.AuthURL = tc.region, tc.authURL
		setDefaultConfig(cc)
		if cc.Global.AuthURL != tc.want || cc.Global.Cloud != "myhuaweicloud.com" {
			t.Fatalf("region %q: auth-url %q cloud %q, want %q", tc.region, cc.Global.AuthURL, cc.Global.Cloud, tc.want)
		}
	}
}
