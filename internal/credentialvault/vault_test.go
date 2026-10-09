package credentialvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == HelperFlag {
		os.Exit(Serve(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

type memoryStore struct {
	mu    sync.Mutex
	data  map[string][]byte
	calls int
}

func (s *memoryStore) Put(_ context.Context, k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[k] = append([]byte(nil), v...)
	return nil
}
func (s *memoryStore) Get(_ context.Context, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	v, ok := s.data[k]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s *memoryStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if _, ok := s.data[k]; !ok {
		return ErrNotFound
	}
	delete(s.data, k)
	return nil
}
func target(t *testing.T) *Target {
	t.Helper()
	v, e := Bind(&rest.Config{Host: "https://cluster.example"}, "production", "operator")
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestVaultBindingRejectsMixedAndUnsafeIdentities(t *testing.T) {
	cases := map[string]func(*rest.Config){"http": func(c *rest.Config) { c.Host = "http://example.com" }, "userinfo": func(c *rest.Config) { c.Host = "https://user@example.com" }, "query": func(c *rest.Config) { c.Host += "?q=1" }, "fragment": func(c *rest.Config) { c.Host += "#x" }, "insecure": func(c *rest.Config) { c.Insecure = true }, "ca-file": func(c *rest.Config) { c.CAFile = "never-read" }, "bad-ca": func(c *rest.Config) { c.CAData = []byte("not pem") }, "token": func(c *rest.Config) { c.BearerToken = "secret" }, "token-file": func(c *rest.Config) { c.BearerTokenFile = "never-read" }, "exec": func(c *rest.Config) { c.ExecProvider = &clientcmdapi.ExecConfig{} }, "auth": func(c *rest.Config) { c.AuthProvider = &clientcmdapi.AuthProviderConfig{} }, "cert": func(c *rest.Config) { c.CertData = []byte("secret") }, "key": func(c *rest.Config) { c.KeyFile = "never-read" }, "basic": func(c *rest.Config) { c.Password = "secret" }, "impersonation": func(c *rest.Config) { c.Impersonate.Groups = []string{"admin"} }, "wrap": func(c *rest.Config) { c.WrapTransport = func(r http.RoundTripper) http.RoundTripper { return r } }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := &rest.Config{Host: "https://cluster.example"}
			change(c)
			if _, e := Bind(c, "production", "operator"); !errors.Is(e, ErrInvalid) {
				t.Fatal("unsafe binding accepted")
			}
		})
	}
}
func TestVaultTargetIsolationAndSnapshot(t *testing.T) {
	c := &rest.Config{Host: "https://cluster.example"}
	a, e := Bind(c, "prod", "user")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ host, name, user, tls string }{{"https://other.example", "prod", "user", ""}, {c.Host, "other", "user", ""}, {c.Host, "prod", "other", ""}, {c.Host, "prod", "user", "other.example"}} {
		b, e := Bind(&rest.Config{Host: v.host, TLSClientConfig: rest.TLSClientConfig{ServerName: v.tls}}, v.name, v.user)
		if e != nil || a.Key() == b.Key() {
			t.Fatal("identity aliasing")
		}
	}
	c.Host = "https://wrong.example"
	if a.config.Host != a.Server {
		t.Fatal("source mutation changed target")
	}
	store := &memoryStore{}
	if e = a.Put(context.Background(), store, "token", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	b := target(t)
	if _, e = b.Resolve(context.Background(), store); !errors.Is(e, ErrNotFound) {
		t.Fatal("another context loaded a stored token")
	}
}
func TestVaultRecordStrictnessExpiryAndBoundedToken(t *testing.T) {
	key := target(t).Key()
	now := time.Now()
	good, _ := json.Marshal(record{1, key, "safe-token", now.Add(time.Hour)})
	if _, e := decode(good, key, now); e != nil {
		t.Fatal(e)
	}
	for _, data := range [][]byte{append(append([]byte(nil), good...), []byte("{}")...), []byte(strings.Replace(string(good), `"version":1`, `"version":1,"version":1`, 1)), []byte(strings.Replace(string(good), `"version":1`, `"version":1,"extra":1`, 1)), []byte(strings.Replace(string(good), "safe-token", "bad token", 1)), []byte(strings.Repeat("a", MaxRecordBytes+1))} {
		if _, e := decode(data, key, now); e == nil {
			t.Fatal("ambiguous credential accepted")
		}
	}
	if _, e := decode(good, key, now.Add(2*time.Hour)); !errors.Is(e, ErrExpired) {
		t.Fatal("expiry ignored")
	}
	if _, e := decode(good, strings.Repeat("0", 64), now); !errors.Is(e, ErrInvalid) {
		t.Fatal("binding mismatch accepted")
	}
	for _, token := range []string{"", strings.Repeat("a", MaxTokenBytes+1), "bad\nheader", "bad\x00token", "汉字"} {
		if validToken(token) {
			t.Fatal("invalid token accepted")
		}
	}
	s := &memoryStore{}
	if e := target(t).Put(context.Background(), s, "safe", now.Add(25*time.Hour)); e == nil || s.calls != 0 {
		t.Fatal("expiry budget not enforced before storage")
	}
}
func TestVaultConnectionLeaseRejectsOldBackendCopies(t *testing.T) {
	s := &memoryStore{}
	v := target(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := v.Put(ctx, s, "stored-token", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	r, e := v.Resolve(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	if r.ExpiresAt.After(time.Now().Add(credentialexec.MaxConnectionLifetime)) {
		t.Fatal("connection lifetime unbounded")
	}
	calls := 0
	transport := r.Config.WrapTransport(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}))
	cancel()
	req, _ := http.NewRequest("GET", "https://cluster.example", nil)
	if _, e = transport.RoundTrip(req); !errors.Is(e, credentialexec.ErrClosed) || calls != 0 {
		t.Fatal("closed credential reached transport")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestVaultHelperRejectsAmbiguousInputAndSanitizesFailures(t *testing.T) {
	key := strings.Repeat("a", 64)
	valid, _ := json.Marshal(request{Operation: "get", Key: key})
	for _, data := range [][]byte{[]byte("{}"), []byte(`{"operation":"get","operation":"delete","key":"` + key + `"}`), append(append([]byte(nil), valid...), []byte("{}")...), []byte(strings.Repeat("a", 4097))} {
		var out bytes.Buffer
		calls := 0
		if code := serve(bytes.NewReader(data), &out, func(request) ([]byte, error) { calls++; return nil, nil }); code == 0 || calls != 0 || out.Len() != 0 {
			t.Fatal("invalid helper payload accepted")
		}
	}
	var out bytes.Buffer
	code := serve(bytes.NewReader(valid), &out, func(request) ([]byte, error) { return nil, errors.New("PRIVATE-TOKEN-IN-NATIVE-ERROR") })
	if code != 0 || strings.Contains(out.String(), "PRIVATE") || !strings.Contains(out.String(), "failed") {
		t.Fatal("native error exposed")
	}
	t.Setenv("ASTER_PARENT_SECRET", "never-in-child")
	t.Setenv("LD_PRELOAD", "never-in-child")
	t.Setenv("KUBERNETES_EXEC_INFO", "never-in-child")
	for _, e := range helperEnvironment() {
		if strings.Contains(e, "never-in-child") {
			t.Fatal("sensitive environment inherited")
		}
	}
}
func TestVaultCanceledRequestDoesNotInvokeOSStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Client{}).Get(ctx, strings.Repeat("a", 64)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
