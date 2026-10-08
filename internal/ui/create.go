package uiworkbench

import (
 "context"
 "strings"
 "github.com/laojianzi/aster/internal/manifest"
 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)
func(w *Workbench)operationContext()context.Context{if w.connectionCtx!=nil{return w.connectionCtx};return w.ctx}
func(w *Workbench)confirmationName()string{if w.plan==nil{return ""};return w.plan.Target().Name}
func(w *Workbench)beginCreate(){
 if w.backend==nil{return};ns:=strings.TrimSpace(w.namespace)
 if w.currentKind.Namespaced&&(ns==""||ns=="*"||ns!=w.activeNamespace){w.errText="Apply a single namespace scope before creating a resource.";return}
 if !w.currentKind.Namespaced{ns=""}
 if w.currentKind.GVR.Resource=="secrets"{w.errText="Secret creation is disabled in the desktop editor.";return}
 w.clearDetail();metadata:=map[string]interface{}{"name":"example"};if ns!=""{metadata["namespace"]=ns}
 obj:=&unstructured.Unstructured{Object:map[string]interface{}{"apiVersion":w.currentKind.GVR.GroupVersion().String(),"kind":w.currentKind.Kind,"metadata":metadata}}
 if w.currentKind.GVR.Resource=="configmaps"{obj.Object["data"]=map[string]interface{}{"example":"value"}}
 text,err:=manifest.Display(obj,false);if err!=nil{w.errText=err.Error();return}
 w.detail=obj;w.detailKind=w.currentKind;w.creating=true;w.editor=text;w.detailText=text;w.detailMode="Edit"
 w.detailMessage="Create-only: review the server dry-run before submission. An existing resource will never be overwritten."
}
