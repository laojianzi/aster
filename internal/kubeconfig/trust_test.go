package kubeconfig

import (
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestTrustIsBoundToExactConfiguration(t *testing.T){
 path:=filepath.Join(t.TempDir(),"config")
 text:="apiVersion: v1\nkind: Config\ncurrent-context: local\nclusters:\n- name: cluster\n  cluster:\n    server: https://127.0.0.1:6443\n    insecure-skip-tls-verify: true\ncontexts:\n- name: local\n  context:\n    cluster: cluster\n    user: user\nusers:\n- name: user\n  user:\n    token: test-token-not-a-secret\n"
 if err:=os.WriteFile(path,[]byte(text),0600);err!=nil{t.Fatal(err)}
 _,err:=Load(Options{Path:path});var first *TrustRequiredError
 if !errors.As(err,&first)||first.Fingerprint==""{t.Fatalf("expected fingerprint: %v",err)}
 if _,err=Load(Options{Path:path,TrustToken:first.Fingerprint});err!=nil{t.Fatal(err)}
 if err=os.WriteFile(path,[]byte(strings.Replace(text,"6443","7443",1)),0600);err!=nil{t.Fatal(err)}
 _,err=Load(Options{Path:path,TrustToken:first.Fingerprint});var second *TrustRequiredError
 if !errors.As(err,&second)||second.Fingerprint==first.Fingerprint{t.Fatalf("changed endpoint retained trust: %v",err)}
}
func TestPlaintextTransportRequiresTrustAndInvalidSchemeFails(t *testing.T){
 path:=filepath.Join(t.TempDir(),"config")
 text:="apiVersion: v1\nkind: Config\ncurrent-context: local\nclusters:\n- name: cluster\n  cluster:\n    server: http://127.0.0.1:8080\ncontexts:\n- name: local\n  context:\n    cluster: cluster\n    user: user\nusers:\n- name: user\n  user:\n    token: test-token-not-a-secret\n"
 if err:=os.WriteFile(path,[]byte(text),0600);err!=nil{t.Fatal(err)}
 _,err:=Load(Options{Path:path});var trust *TrustRequiredError
 if !errors.As(err,&trust){t.Fatalf("plaintext credentials accepted: %v",err)}
 if err:=os.WriteFile(path,[]byte(strings.Replace(text,"http://","file://",1)),0600);err!=nil{t.Fatal(err)}
 if _,err:=Load(Options{Path:path,TrustToken:trust.Fingerprint});err==nil{t.Fatal("unsupported scheme accepted")}
}
