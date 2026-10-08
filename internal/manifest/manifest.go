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
	if len(data)==0 || len(data)>MaxBytes || bytes.Count(data,[]byte("\n"))>5000{return nil,errors.New("manifest must contain one document, at most 256 KiB / 5000 lines")}
	dec:=utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data),4096)
	var first,extra runtime.RawExtension
	if err:=dec.Decode(&first);err!=nil{return nil,err}
	if err:=dec.Decode(&extra);err!=io.EOF{return nil,errors.New("multiple YAML documents are not supported by the single-resource editor")}
	strict,err:=yaml.YAMLToJSONStrict(data);if err!=nil{return nil,err}
	o:=&unstructured.Unstructured{};if err=o.UnmarshalJSON(strict);err!=nil{return nil,err}
	if o.GetAPIVersion()==""||o.GetKind()==""||o.GetName()==""{return nil,errors.New("apiVersion, kind and metadata.name are required")}
	return o,nil
}

// RedactedCopy is used by read-only views and operation previews, never by writes.
func RedactedCopy(obj *unstructured.Unstructured,reveal bool)*unstructured.Unstructured{
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

type Change struct{ Path,Before,After string }
func Compare(before,after []byte)([]Change,error){
	var a,b interface{};if err:=json.Unmarshal(before,&a);err!=nil{return nil,err};if err:=json.Unmarshal(after,&b);err!=nil{return nil,err}
	var out []Change
	var visit func(string,interface{},bool,interface{},bool)error
	visit=func(path string,a interface{},hasA bool,b interface{},hasB bool)error{
		am,aok:=a.(map[string]interface{});bm,bok:=b.(map[string]interface{})
		if aok&&bok{keys:=map[string]bool{};for k:=range am{keys[k]=true};for k:=range bm{keys[k]=true};sorted:=make([]string,0,len(keys));for k:=range keys{sorted=append(sorted,k)};sort.Strings(sorted);for _,k:=range sorted{av,ap:=am[k];bv,bp:=bm[k];if err:=visit(path+"/"+strings.ReplaceAll(strings.ReplaceAll(k,"~","~0"),"/","~1"),av,ap,bv,bp);err!=nil{return err}};return nil}
		av,_:=json.Marshal(a);bv,_:=json.Marshal(b)
		if hasA!=hasB||!bytes.Equal(av,bv){if len(out)>=1000{return errors.New("diff exceeds 1000 changed fields")};left,right:=string(av),string(bv);if !hasA{left="<absent>"};if !hasB{right="<absent>"};out=append(out,Change{path,left,right})};return nil
	}
	if err:=visit("",a,true,b,true);err!=nil{return nil,err};return out,nil
}
func Summary(changes []Change)string{var b strings.Builder;for _,c:=range changes{fmt.Fprintf(&b,"%s\n- %s\n+ %s\n\n",c.Path,c.Before,c.After)};return b.String()}
