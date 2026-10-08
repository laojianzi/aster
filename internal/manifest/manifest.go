package manifest

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "sort"
 "strings"

 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
 "k8s.io/apimachinery/pkg/runtime"
 utilyaml "k8s.io/apimachinery/pkg/util/yaml"
 "sigs.k8s.io/yaml"
)

const MaxBytes=256<<10
func Decode(data []byte)(*unstructured.Unstructured,error){
 if len(data)==0||len(data)>MaxBytes||bytes.Count(data,[]byte("\n"))>5000{return nil,errors.New("manifest must contain one document, at most 256 KiB / 5000 lines")}
 dec:=utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data),4096)
 var first,extra runtime.RawExtension
 if err:=dec.Decode(&first);err!=nil{return nil,err}
 if err:=dec.Decode(&extra);err!=io.EOF{return nil,errors.New("multiple YAML documents are not supported by the single-resource editor")}
 strict,err:=yaml.YAMLToJSONStrict(data);if err!=nil{return nil,err}
 o:=&unstructured.Unstructured{};if err=o.UnmarshalJSON(strict);err!=nil{return nil,err}
 if o.GetAPIVersion()==""||o.GetKind()==""||o.GetName()==""{return nil,errors.New("apiVersion, kind and metadata.name are required")}
 return o,nil
}
// RedactedCopy is for views, never for writes.
func RedactedCopy(obj *unstructured.Unstructured,reveal bool)*unstructured.Unstructured{
 if obj==nil{return nil}
 copy:=obj.DeepCopy();copy.SetManagedFields(nil)
 annotations:=copy.GetAnnotations();delete(annotations,"kubectl.kubernetes.io/last-applied-configuration");copy.SetAnnotations(annotations)
 if copy.GetKind()=="Secret"&&!reveal{for _,field:=range []string{"data","stringData"}{if values,ok:=copy.Object[field].(map[string]interface{});ok{for key:=range values{values[key]="<redacted>"}}}}
 return copy
}
func Display(obj *unstructured.Unstructured,reveal bool)(string,error){
 if obj==nil{return "",errors.New("nil resource")}
 data,err:=RedactedCopy(obj,reveal).MarshalJSON();if err!=nil{return "",err}
 data,err=yaml.JSONToYAML(data);if err!=nil{return "",err}
 if len(data)>MaxBytes{return "",errors.New("resource exceeds editor/viewer byte budget; use an external tool")}
 return string(data),nil
}

type Change struct{Path,Before,After string}
var volatileMetadata=[]string{"creationTimestamp","generation","managedFields","resourceVersion","uid"}

// Detect differences before redacting them. Whole-object deletion must not
// serialize Secret payloads, while changed Secret fields remain visible.
func Compare(before,after []byte)([]Change,error){
 if len(before)>MaxBytes||len(after)>MaxBytes{return nil,errors.New("diff input exceeds 256 KiB")}
 var a,b interface{}
 if err:=json.Unmarshal(before,&a);err!=nil{return nil,err};if err:=json.Unmarshal(after,&b);err!=nil{return nil,err}
 secret:=isSecret(a)||isSecret(b);normalizeDiff(a);normalizeDiff(b)
 var out []Change;size:=0
 var visit func(string,interface{},bool,interface{},bool)error
 visit=func(path string,a interface{},hasA bool,b interface{},hasB bool)error{
  am,aok:=a.(map[string]interface{});bm,bok:=b.(map[string]interface{})
  if aok&&bok{
   keys:=map[string]bool{};for k:=range am{keys[k]=true};for k:=range bm{keys[k]=true}
   sorted:=make([]string,0,len(keys));for k:=range keys{sorted=append(sorted,k)};sort.Strings(sorted)
   for _,k:=range sorted{av,ap:=am[k];bv,bp:=bm[k];escaped:=strings.ReplaceAll(strings.ReplaceAll(k,"~","~0"),"/","~1");if err:=visit(path+"/"+escaped,av,ap,bv,bp);err!=nil{return err}}
   return nil
  }
  av,err:=json.Marshal(a);if err!=nil{return err};bv,err:=json.Marshal(b);if err!=nil{return err}
  if hasA==hasB&&bytes.Equal(av,bv){return nil}
  if len(out)>=1000{return errors.New("diff exceeds 1000 changed fields")}
  left,right:=string(av),string(bv);if secret{left=renderRedacted(path,a);right=renderRedacted(path,b)}
  if !hasA{left="<absent>"};if !hasB{right="<absent>"}
  size+=len(path)+len(left)+len(right);if size>MaxBytes{return errors.New("diff output exceeds 256 KiB; narrow the change")}
  if path==""{path="/"};out=append(out,Change{path,left,right});return nil
 }
 if err:=visit("",a,true,b,true);err!=nil{return nil,err};return out,nil
}
func isSecret(v interface{})bool{m,ok:=v.(map[string]interface{});return ok&&m["kind"]=="Secret"}
func normalizeDiff(v interface{}){
 obj,ok:=v.(map[string]interface{});if !ok{return};metadata,ok:=obj["metadata"].(map[string]interface{});if !ok{return}
 for _,key:=range volatileMetadata{delete(metadata,key)}
 if annotations,ok:=metadata["annotations"].(map[string]interface{});ok{delete(annotations,"kubectl.kubernetes.io/last-applied-configuration");if len(annotations)==0{delete(metadata,"annotations")}}
}
func renderRedacted(path string,v interface{})string{
 if path=="/data"||strings.HasPrefix(path,"/data/")||path=="/stringData"||strings.HasPrefix(path,"/stringData/"){return "\"<redacted>\""}
 if path==""{if obj,ok:=v.(map[string]interface{});ok{copy:=make(map[string]interface{},len(obj));for k,x:=range obj{copy[k]=x};for _,key:=range []string{"data","stringData"}{if _,ok:=copy[key];ok{copy[key]="<redacted>"}};v=copy}}
 data,_:=json.Marshal(v);return string(data)
}
func Summary(changes []Change)string{var b strings.Builder;for _,c:=range changes{fmt.Fprintf(&b,"%s\n- %s\n+ %s\n\n",c.Path,c.Before,c.After)};return b.String()}
