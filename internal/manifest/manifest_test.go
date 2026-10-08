package manifest

import (
	"strings"
	"testing"
)
func TestDecodeAndRejectAmbiguity(t *testing.T){
	valid:="apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\ndata:\n  greeting: 你好\n"
	o,err:=Decode([]byte(valid));if err!=nil||o.GetName()!="example"{t.Fatal(err)}
	for _,bad:=range []string{valid+"---\n"+valid,valid+"kind: Pod\n",strings.Repeat("x",MaxBytes+1),"{}"}{if _,err:=Decode([]byte(bad));err==nil{t.Fatal("invalid manifest accepted")}}
}
func TestSecretIsRedactedWithoutMutatingOriginal(t *testing.T){
	o,err:=Decode([]byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: secret\ndata:\n  password: YWJj\n"));if err!=nil{t.Fatal(err)}
	text,err:=Display(o,false);if err!=nil||strings.Contains(text,"YWJj"){t.Fatal("secret leaked")}
	revealed,_:=Display(o,true);if !strings.Contains(revealed,"YWJj"){t.Fatal("original was mutated")}
}
func TestDiffStablePaths(t *testing.T){changes,err:=Compare([]byte(`{"data":{"a/b":"before"}}`),[]byte(`{"data":{"a/b":"after"}}`));if err!=nil||len(changes)!=1||changes[0].Path!="/data/a~1b"{t.Fatalf("%v %v",changes,err)}}
