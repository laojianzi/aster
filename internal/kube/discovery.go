package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceKind struct { GVR schema.GroupVersionResource; Kind string; Namespaced bool }
func (k ResourceKind) Label() string { if k.GVR.Group == "" { return k.Kind }; return k.Kind+" ("+k.GVR.Group+")" }

// Discover tolerates individual unavailable aggregated APIs. Every request has a
// deadline and at most four API groups are requested at once.
func (b *Backend) Discover(ctx context.Context) ([]ResourceKind,[]string,error) {
	ctx,cancel := context.WithTimeout(ctx,45*time.Second); defer cancel()
	client := b.typed.CoreV1().RESTClient()
	var groups metav1.APIGroupList
	if err := client.Get().AbsPath("/apis").Do(ctx).Into(&groups); err != nil { return nil,nil,err }
	paths := []string{"/api/v1"}
	if len(groups.Groups)>128 { return nil,nil,fmt.Errorf("too many API groups") }
	for _,group := range groups.Groups { if group.PreferredVersion.GroupVersion != "" { paths=append(paths,"/apis/"+group.PreferredVersion.GroupVersion) } }
	var mu sync.Mutex; var wg sync.WaitGroup
	sem := make(chan struct{},4)
	var out []ResourceKind; var warnings []string
requests:
	for _,path := range paths {
		select { case sem<-struct{}{}: case <-ctx.Done(): break requests }
		wg.Add(1)
		go func(path string) {
			defer wg.Done(); defer func(){<-sem}()
			requestCtx,done := context.WithTimeout(ctx,10*time.Second); defer done()
			var list metav1.APIResourceList
			err := client.Get().AbsPath(path).Do(requestCtx).Into(&list)
			mu.Lock(); defer mu.Unlock()
			if err != nil { warnings=append(warnings,path+": unavailable"); return }
			gv,err := schema.ParseGroupVersion(list.GroupVersion); if err != nil { warnings=append(warnings,path+": invalid version"); return }
			for _,r := range list.APIResources {
				if strings.Contains(r.Name,"/") || !hasVerb(r.Verbs,"list") || !hasVerb(r.Verbs,"watch") { continue }
				out=append(out,ResourceKind{GVR:gv.WithResource(r.Name),Kind:r.Kind,Namespaced:r.Namespaced})
			}
		}(path)
	}
	wg.Wait()
	if ctx.Err()!=nil { return nil,warnings,ctx.Err() }
	sort.Slice(out,func(i,j int)bool{return out[i].Label()<out[j].Label()}); sort.Strings(warnings)
	return out,warnings,nil
}
func hasVerb(verbs []string,v string)bool { for _,item:=range verbs { if item==v{return true} };return false }
