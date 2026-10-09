// Package testschema contains synthetic test fixtures only. Product code must
// obtain the selected GVK schema from its current authenticated API server.
package testschema

const Index = `{"paths":{"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1?hash=AB12"}}}`
const Document = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","required":["containers"],"properties":{
     "containers":{"type":"array","items":{"allOf":[{"$ref":"#/components/schemas/Container"}],"description":"A workload container."}},
     "count":{"type":"integer"},"maybe":{"type":"string","nullable":true},
     "opaque":{"type":"object","x-kubernetes-preserve-unknown-fields":true,"properties":{"known":{"type":"integer"}}},
     "labels":{"type":"object","additionalProperties":{"type":"string"}},
     "port":{"x-kubernetes-int-or-string":true,"anyOf":[{"type":"integer"},{"type":"string"}]},
     "special/key~":{"type":"string"}
    }}
   }},
  "Container":{"type":"object","required":["name","image"],"properties":{"name":{"type":"string"},"image":{"type":"string","description":"Container image reference."}}}
 }}}
`
