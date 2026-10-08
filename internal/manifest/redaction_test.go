package manifest

import("strings";"testing")

func TestDiffPreservesSecretChangesWithoutLeakingValues(t *testing.T){
 before:=[]byte(`{"kind":"Secret","data":{"password":"OLD-SENTINEL"},"metadata":{"resourceVersion":"1","annotations":{"kubectl.kubernetes.io/last-applied-configuration":"ANNOTATION-SENTINEL"}}}`)
 after:=[]byte(`{"kind":"Secret","data":{"password":"NEW-SENTINEL"},"metadata":{"resourceVersion":"2"}}`)
 for _,right:=range [][]byte{after,[]byte("null")}{changes,err:=Compare(before,right);if err!=nil{t.Fatal(err)};text:=Summary(changes);if len(changes)!=1||!strings.Contains(text,"redacted"){t.Fatalf("missing change: %s",text)};for _,value:=range []string{"OLD-SENTINEL","NEW-SENTINEL","ANNOTATION-SENTINEL"}{if strings.Contains(text,value){t.Fatal("Secret leaked in diff")}}}
}
func TestDiffIgnoresServerManagedMetadata(t *testing.T){
 changes,err:=Compare([]byte(`{"metadata":{"name":"same","uid":"u","resourceVersion":"1","generation":1}}`),[]byte(`{"metadata":{"name":"same","uid":"u","resourceVersion":"2","generation":2}}`))
 if err!=nil||len(changes)!=0{t.Fatalf("volatile metadata diff: %v %v",changes,err)}
}
func TestDiffBoundsInputs(t *testing.T){if _,err:=Compare([]byte(strings.Repeat(" ",MaxBytes+1)),[]byte("null"));err==nil{t.Fatal("unbounded diff accepted")}}
