package resource

import (
 "fmt"
 "k8s.io/apimachinery/pkg/runtime/schema"
 "k8s.io/apimachinery/pkg/types"
)

type Identity struct {SessionID string;GVR schema.GroupVersionResource;Namespace,Name string;UID types.UID}
func(i Identity)Validate()error{
 if i.SessionID==""{return fmt.Errorf("resource: empty session id")}
 if i.GVR.Version==""{return fmt.Errorf("resource: empty API version")}
 if i.GVR.Resource==""{return fmt.Errorf("resource: empty resource")}
 if i.Name==""{return fmt.Errorf("resource: empty name")}
 return nil
}
func(i Identity)Key()string{return i.SessionID+"|"+i.GVR.String()+"|"+i.Namespace+"|"+i.Name+"|"+string(i.UID)}
