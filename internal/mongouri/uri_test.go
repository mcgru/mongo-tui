package mongouri

import "testing"

func TestParseMultiHost(t *testing.T) {
	const raw = `mongodb://uuu:pppp@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs`
	u, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if u.Scheme != "mongodb" || u.User != "uuu" || !u.HasPassword {
		t.Fatalf("parsed = %+v", u)
	}
	if u.Password != "pppp" {
		t.Fatalf("password = %q", u.Password)
	}
	if u.Host != "mongod1:27021,mongod2:27022,mongod3:27023" {
		t.Fatalf("host = %q", u.Host)
	}
	if u.Database() != "stage" || u.Query != "authSource=admin&replicaset=dbrs" {
		t.Fatalf("db/query = %q/%q", u.Database(), u.Query)
	}
	if got, want := u.WithoutPassword(), `mongodb://uuu@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs`; got != want {
		t.Fatalf("WithoutPassword() = %q, want %q", got, want)
	}
	if got := u.Label(); got != "uuu@mongod1:27021,mongod2:27022,mongod3:27023/stage" {
		t.Fatalf("Label() = %q", got)
	}
	if got := u.Endpoint(); got != "uuu@mongod1:27021,mongod2:27022,mongod3:27023" {
		t.Fatalf("Endpoint() = %q", got)
	}
}

func TestParseWithoutUserAndSRV(t *testing.T) {
	u, err := Parse("mongodb+srv://cluster.example/app?retryWrites=true")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if u.Scheme != "mongodb+srv" || u.User != "" || u.HasPassword {
		t.Fatalf("parsed = %+v", u)
	}
	if u.Database() != "app" || u.Host != "cluster.example" {
		t.Fatalf("host/db = %q/%q", u.Host, u.Database())
	}
}

func TestParsePlaceholderUserinfo(t *testing.T) {
	u, err := Parse(`mongodb://<credentials>@host1:27017,host2:27017/db`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if u.User != "<credentials>" || u.HasPassword || u.Password != "" {
		t.Fatalf("parsed = %+v", u)
	}
}

func TestParseDecodesPassword(t *testing.T) {
	u, err := Parse(`mongodb://ann:p%40ss%3Aword@db.example:27017/app`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if u.User != "ann" || u.Password != "p@ss:word" {
		t.Fatalf("user/password = %q/%q", u.User, u.Password)
	}
}

func TestParseRejectsBadURIs(t *testing.T) {
	for _, raw := range []string{
		`postgresql://db/app`,
		`mongodb://`,
		`not-a-uri`,
	} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) = nil, want an error", raw)
		}
	}
}
