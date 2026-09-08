package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io/ioutil"
	"log"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/WallfacerTech/openapi-cli-generator/shorthand"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"
)

//go:generate go-bindata ./templates/...

// OpenAPI Extensions
const (
	ExtAliases     = "x-cli-aliases"
	ExtDescription = "x-cli-description"
	ExtGroup       = "x-cli-group"
	ExtIgnore      = "x-cli-ignore"
	ExtHidden      = "x-cli-hidden"
	ExtName        = "x-cli-name"
	ExtWaiters     = "x-cli-waiters"
)

// Param describes an OpenAPI parameter (path, query, header, etc)
type Param struct {
	Name        string
	CLIName     string
	GoName      string
	Description string
	In          string
	Required    bool
	Type        string
	TypeNil     string
	Style       string
	Explode     bool
}

// Operation describes an OpenAPI operation (GET/POST/PUT/PATCH/DELETE)
type Operation struct {
	HandlerName    string
	GoName         string
	Use            string
	Aliases        []string
	Short          string
	Long           string
	Method         string
	CanHaveBody    bool
	ReturnType     string
	Path           string
	Group          string
	AllParams      []*Param
	RequiredParams []*Param
	OptionalParams []*Param
	MediaType      string
	Examples       []string
	Hidden         bool
	NeedsResponse  bool
	Waiters        []*WaiterParams
}

// OperationGroup holds operations that share a resource group.
type OperationGroup struct {
	Name       string
	GoName     string
	Short      string
	Operations []*Operation
}

// Waiter describes a special command that blocks until a condition has been
// met, after which it exits.
type Waiter struct {
	CLIName     string
	GoName      string
	Use         string
	Aliases     []string
	Short       string
	Long        string
	Delay       int
	Attempts    int
	OperationID string `json:"operationId"`
	Operation   *Operation
	Matchers    []*Matcher
	After       map[string]map[string]string
}

// Matcher describes a condition to match for a waiter.
type Matcher struct {
	Select   string
	Test     string
	Expected json.RawMessage
	State    string
}

// WaiterParams links a waiter with param selector querires to perform wait
// operations after a command has run.
type WaiterParams struct {
	Waiter *Waiter
	Args   []string
	Params map[string]string
}

// Server describes an OpenAPI server endpoint
type Server struct {
	Description string
	URL         string
	// TODO: handle server parameters
}

// Imports describe optional imports based on features in use.
type Imports struct {
	Fmt     bool
	Strings bool
	Time    bool
}

// OpenAPI describes an API
type OpenAPI struct {
	Imports      Imports
	Name         string
	GoName       string
	PublicGoName string
	Title        string
	Description  string
	Servers      []*Server
	Operations   []*Operation
	Groups       []*OperationGroup
	Waiters      []*Waiter
}

// ProcessAPI returns the API description to be used with the commands template
// for a loaded and dereferenced OpenAPI 3 document.
func ProcessAPI(shortName string, api *openapi3.Swagger) *OpenAPI {
	apiName := shortName
	if api.Info.Extensions[ExtName] != nil {
		apiName = extStr(api.Info.Extensions[ExtName])
	}

	apiDescription := api.Info.Description
	if api.Info.Extensions[ExtDescription] != nil {
		apiDescription = extStr(api.Info.Extensions[ExtDescription])
	}

	result := &OpenAPI{
		Name:         apiName,
		GoName:       toGoName(shortName, false),
		PublicGoName: toGoName(shortName, true),
		Title:        api.Info.Title,
		Description:  escapeString(apiDescription),
	}

	for _, s := range api.Servers {
		result.Servers = append(result.Servers, &Server{
			Description: s.Description,
			URL:         s.URL,
		})
	}

	// Convenience map for operation ID -> operation
	operationMap := make(map[string]*Operation)

	// Go names already taken by an operation. Operation IDs are not unique
	// across paths in every spec, and the name is emitted as a package-level
	// func, so repeats have to be qualified.
	usedGoNames := make(map[string]bool)

	var keys []string
	for path := range api.Paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	collectionResources, multiParentResources := precomputeResources(keys)

	// Which operation keeps the plain command name inside each group, for the
	// groups where two operations derive the same one.
	actionOwners := precomputeActionOwners(keys, api, collectionResources, multiParentResources)

	for _, path := range keys {
		item := api.Paths[path]

		if item.Extensions[ExtIgnore] != nil {
			// Ignore this path.
			continue
		}

		pathHidden := false
		if item.Extensions[ExtHidden] != nil {
			json.Unmarshal(item.Extensions[ExtHidden].(json.RawMessage), &pathHidden)
		}

		// Operations come back in a map, so walk them in a fixed order:
		// otherwise the generated file, and which of two operations sharing a
		// name gets qualified, changes from run to run.
		operations := item.Operations()

		var methods []string
		for method := range operations {
			methods = append(methods, method)
		}
		sort.Strings(methods)

		for _, method := range methods {
			operation := operations[method]

			if operation.Extensions[ExtIgnore] != nil {
				// Ignore this operation.
				continue
			}

			name := operation.OperationID
			if operation.Extensions[ExtName] != nil {
				name = extStr(operation.Extensions[ExtName])
			}

			var aliases []string
			if operation.Extensions[ExtAliases] != nil {
				// We need to decode the raw extension value into our string slice.
				json.Unmarshal(operation.Extensions[ExtAliases].(json.RawMessage), &aliases)
			}

			params := getParams(item, method)
			requiredParams := getRequiredParams(params)
			optionalParams := getOptionalParams(params)
			short := operation.Summary
			if short == "" {
				short = name
			}
			// Spec summaries may span multiple lines and contain quotes. This
			// value is emitted into both a Go doc comment and Go string
			// literals, so collapse it to a single line and escape it.
			short = escapeString(strings.Join(strings.Fields(short), " "))

			use := usage(name, requiredParams)

			description := operation.Description
			if operation.Extensions[ExtDescription] != nil {
				description = extStr(operation.Extensions[ExtDescription])
			}

			reqMt, reqSchema, reqExamples := getRequestInfo(operation)

			var examples []string
			if len(reqExamples) > 0 {
				wroteHeader := false
				for _, ex := range reqExamples {
					if _, ok := ex.(string); !ok {
						// Not a string, so it's structured data. Let's marshal it to the
						// shorthand syntax if we can.
						if m, ok := ex.(map[string]interface{}); ok {
							ex = shorthand.Get(m)
							examples = append(examples, ex.(string))
							continue
						}

						b, _ := json.Marshal(ex)

						if !wroteHeader {
							description += "\n## Input Example\n\n"
							wroteHeader = true
						}

						description += "\n" + string(b) + "\n"
						continue
					}

					if !wroteHeader {
						description += "\n## Input Example\n\n"
						wroteHeader = true
					}

					description += "\n" + ex.(string) + "\n"
				}
			}

			if reqSchema != "" {
				description += "\n## Request Schema (" + reqMt + ")\n\n" + reqSchema
			}

			method := strings.Title(strings.ToLower(method))

			hidden := pathHidden
			if operation.Extensions[ExtHidden] != nil {
				json.Unmarshal(operation.Extensions[ExtHidden].(json.RawMessage), &hidden)
			}

			returnType := "interface{}"
		returnTypeLoop:
			for code, ref := range operation.Responses {
				if num, err := strconv.Atoi(code); err != nil || num < 200 || num >= 300 {
					// Skip invalid responses
					continue
				}

				if ref.Value != nil {
					for _, content := range ref.Value.Content {
						if _, ok := content.Example.(map[string]interface{}); ok {
							returnType = "map[string]interface{}"
							break returnTypeLoop
						}

						if content.Schema != nil && content.Schema.Value != nil {
							if content.Schema.Value.Type == "object" || len(content.Schema.Value.Properties) != 0 {
								returnType = "map[string]interface{}"
								break returnTypeLoop
							}
						}
					}
				}
			}

			group, actionName := groupAndAction(operation, path, strings.ToUpper(method), collectionResources, multiParentResources)
			actionName = uniqueAction(path, strings.ToUpper(method), group, actionName, actionOwners)

			use = actionUsage(actionName, requiredParams)

			goName, handlerName := uniqueNames(name, group, usedGoNames)

			o := &Operation{
				HandlerName:    handlerName,
				GoName:         goName,
				Use:            use,
				Aliases:        aliases,
				Short:          short,
				Long:           escapeString(description),
				Method:         method,
				CanHaveBody:    method == "Post" || method == "Put" || method == "Patch",
				ReturnType:     returnType,
				Path:           path,
				Group:          group,
				AllParams:      params,
				RequiredParams: requiredParams,
				OptionalParams: optionalParams,
				MediaType:      reqMt,
				Examples:       examples,
				Hidden:         hidden,
			}

			operationMap[operation.OperationID] = o

			result.Operations = append(result.Operations, o)

			for _, p := range params {
				if p.In == "path" {
					result.Imports.Strings = true
				}
			}

			for _, p := range optionalParams {
				if p.In == "query" || p.In == "header" {
					result.Imports.Fmt = true
				}
			}
		}
	}

	if api.Extensions[ExtWaiters] != nil {
		var waiters map[string]*Waiter

		if err := json.Unmarshal(api.Extensions[ExtWaiters].(json.RawMessage), &waiters); err != nil {
			panic(err)
		}

		for name, waiter := range waiters {
			waiter.CLIName = slug(name)
			waiter.GoName = toGoName(name+"-waiter", true)
			waiter.Operation = operationMap[waiter.OperationID]
			waiter.Use = usage(name, waiter.Operation.RequiredParams)

			for _, matcher := range waiter.Matchers {
				if matcher.Test == "" {
					matcher.Test = "equal"
				}
			}

			for operationID, waitOpParams := range waiter.After {
				op := operationMap[operationID]
				if op == nil {
					panic(fmt.Errorf("Unknown waiter operation %s", operationID))
				}

				var args []string
				for _, p := range op.RequiredParams {
					selector := waitOpParams[p.Name]
					if selector == "" {
						panic(fmt.Errorf("Missing required parameter %s", p.Name))
					}
					delete(waitOpParams, p.Name)

					args = append(args, selector)

					result.Imports.Fmt = true
					op.NeedsResponse = true
				}

				// Transform from OpenAPI param names to CLI names
				wParams := make(map[string]string)
				for p, s := range waitOpParams {
					found := false
					for _, optional := range op.OptionalParams {
						if optional.Name == p {
							wParams[optional.CLIName] = s
							found = true
							break
						}
					}
					if !found {
						panic(fmt.Errorf("Unknown parameter %s for waiter %s", p, name))
					}
				}

				op.Waiters = append(op.Waiters, &WaiterParams{
					Waiter: waiter,
					Args:   args,
					Params: wParams,
				})
			}

			result.Waiters = append(result.Waiters, waiter)
		}

		if len(waiters) > 0 {
			result.Imports.Time = true
		}
	}

	// Build groups from operations
	groupMap := make(map[string]*OperationGroup)
	var groupOrder []string
	for _, op := range result.Operations {
		g := op.Group
		if _, exists := groupMap[g]; !exists {
			groupMap[g] = &OperationGroup{
				Name:   g,
				GoName: toGoName(g, true),
				Short:  "Manage " + g,
			}
			groupOrder = append(groupOrder, g)
		}
		groupMap[g].Operations = append(groupMap[g].Operations, op)
	}
	for _, name := range groupOrder {
		result.Groups = append(result.Groups, groupMap[name])
	}

	return result
}

// extStr returns the string value of an OpenAPI extension stored as a JSON
// raw message.
func extStr(i interface{}) (decoded string) {
	if err := json.Unmarshal(i.(json.RawMessage), &decoded); err != nil {
		panic(err)
	}

	return
}

func toGoName(input string, public bool) string {
	transformed := strings.Replace(input, "-", " ", -1)
	transformed = strings.Replace(transformed, "_", " ", -1)
	transformed = strings.Title(transformed)
	transformed = strings.Replace(transformed, " ", "", -1)

	if !public {
		transformed = strings.ToLower(string(transformed[0])) + transformed[1:]
	}

	return transformed
}

func escapeString(value string) string {
	transformed := strings.Replace(value, "\n", "\\n", -1)
	transformed = strings.Replace(transformed, "\"", "\\\"", -1)
	return transformed
}

func isPathParam(s string) bool {
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

func cleanPathParts(urlPath string) []string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) > 0 && len(parts[0]) >= 2 && parts[0][0] == 'v' && parts[0][1] >= '0' && parts[0][1] <= '9' {
		parts = parts[1:]
	}
	return parts
}

// precomputeResources analyzes all API paths to determine which segments are
// collection resources and which appear under multiple parents.
func precomputeResources(paths []string) (collectionResources, multiParentResources map[string]bool) {
	collectionResources = map[string]bool{}
	parentMap := map[string]map[string]bool{}

	for _, p := range paths {
		parts := cleanPathParts(p)
		var lastCollection string
		for i := 0; i < len(parts); i++ {
			seg := parts[i]
			if isPathParam(seg) {
				continue
			}
			if i+1 < len(parts) && isPathParam(parts[i+1]) {
				collectionResources[seg] = true
				if lastCollection != "" && seg != lastCollection {
					if parentMap[seg] == nil {
						parentMap[seg] = map[string]bool{}
					}
					parentMap[seg][lastCollection] = true
				}
				lastCollection = seg
			}
		}
	}

	multiParentResources = map[string]bool{}
	for res, parents := range parentMap {
		if len(parents) > 1 {
			multiParentResources[res] = true
		}
	}
	return
}

func deriveGroupAndAction(urlPath, httpMethod string, collectionResources, multiParentResources map[string]bool) (group, action string) {
	parts := cleanPathParts(urlPath)

	if len(parts) == 0 {
		return "api", methodToAction(httpMethod, false)
	}

	// Find the deepest qualifying collection resource (not multi-parent)
	groupIdx := -1
	for i := 0; i < len(parts); i++ {
		seg := parts[i]
		if isPathParam(seg) {
			continue
		}
		if collectionResources[seg] && !multiParentResources[seg] {
			groupIdx = i
			group = seg
		}
	}

	if groupIdx < 0 {
		group = parts[0]
		groupIdx = 0
	}

	// Collect action parts: non-param segments after the group and its param
	afterIdx := groupIdx + 1
	if afterIdx < len(parts) && isPathParam(parts[afterIdx]) {
		afterIdx++
	}

	var actionParts []string
	for i := afterIdx; i < len(parts); i++ {
		if !isPathParam(parts[i]) {
			actionParts = append(actionParts, parts[i])
		}
	}

	lastIsParam := isPathParam(parts[len(parts)-1])

	if len(actionParts) > 0 {
		action = strings.Join(actionParts, "-")
		if lastIsParam && len(actionParts) == 1 {
			action = depluralize(actionParts[0])
		}
	} else {
		action = methodToAction(httpMethod, lastIsParam)
	}

	return
}

// groupAndAction returns the command group and action name for an operation,
// honoring the x-cli-group and x-cli-name extensions over what the path
// derives.
func groupAndAction(operation *openapi3.Operation, path, httpMethod string, collectionResources, multiParentResources map[string]bool) (string, string) {
	group, action := deriveGroupAndAction(path, httpMethod, collectionResources, multiParentResources)

	if operation.Extensions[ExtGroup] != nil {
		group = extStr(operation.Extensions[ExtGroup])
	}
	if operation.Extensions[ExtName] != nil {
		action = extStr(operation.Extensions[ExtName])
	}

	return group, action
}

// actionOwner is the operation that keeps a group's plain command name.
type actionOwner struct {
	path   string
	method string
	depth  int
}

// precomputeActionOwners decides, for every group and action name two or more
// operations derive, which operation keeps the plain name. Cobra resolves a
// duplicate command name to whichever command was registered first, so without
// this the canonical route can silently lose its command to a nested one that
// happens to sort earlier: `POST /accounts/{id}/onboardings/{onboarding}/tasks`
// and `POST /accounts/{id}/tasks` both derive `tasks create`. The shallowest
// path wins, ties going to the first path and method in sorted order, so the
// choice is stable across runs.
func precomputeActionOwners(keys []string, api *openapi3.Swagger, collectionResources, multiParentResources map[string]bool) map[string]actionOwner {
	owners := map[string]actionOwner{}

	for _, path := range keys {
		item := api.Paths[path]

		if item.Extensions[ExtIgnore] != nil {
			continue
		}

		operations := item.Operations()

		var methods []string
		for method := range operations {
			methods = append(methods, method)
		}
		sort.Strings(methods)

		for _, method := range methods {
			operation := operations[method]

			if operation.Extensions[ExtIgnore] != nil {
				continue
			}

			group, action := groupAndAction(operation, path, strings.ToUpper(method), collectionResources, multiParentResources)
			candidate := actionOwner{path: path, method: strings.ToUpper(method), depth: len(cleanPathParts(path))}

			if current, seen := owners[group+"\x00"+action]; !seen || candidate.depth < current.depth {
				owners[group+"\x00"+action] = candidate
			}
		}
	}

	return owners
}

// uniqueAction returns the command name this operation may use inside its
// group: the plain action for the operation that owns it, and a qualified one
// for the rest. The qualifier is the HTTP method's own verb where that differs
// from the action (`update-current` for a PUT beside a GET on the same path),
// otherwise the resource the path nests under (`onboarding-create`), and a
// counter if neither is available.
func uniqueAction(path, httpMethod, group, action string, owners map[string]actionOwner) string {
	owner, ok := owners[group+"\x00"+action]
	if !ok || (owner.path == path && owner.method == httpMethod) {
		return action
	}

	parts := cleanPathParts(path)
	lastIsParam := len(parts) > 0 && isPathParam(parts[len(parts)-1])

	if verb := methodToAction(httpMethod, lastIsParam); verb != action {
		return verb + "-" + action
	}

	if parent := parentResource(parts, group); parent != "" {
		return depluralize(parent) + "-" + action
	}

	return action + "-" + httpMethodSuffix(httpMethod)
}

// parentResource returns the non-parameter segment the group's own segment
// nests under, if there is one.
func parentResource(parts []string, group string) string {
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] != group {
			continue
		}

		for j := i - 1; j >= 0; j-- {
			if !isPathParam(parts[j]) {
				return parts[j]
			}
		}
	}

	return ""
}

func httpMethodSuffix(httpMethod string) string {
	return strings.ToLower(httpMethod)
}

func methodToAction(method string, singleResource bool) string {
	switch strings.ToUpper(method) {
	case "GET":
		if singleResource {
			return "get"
		}
		return "list"
	case "POST":
		return "create"
	case "PATCH", "PUT":
		return "update"
	case "DELETE":
		return "delete"
	}
	return strings.ToLower(method)
}

func depluralize(s string) string {
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "ses") || strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "zes") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && !strings.HasSuffix(s, "us") {
		return s[:len(s)-1]
	}
	return s
}

// uniqueNames returns the Go function name and handler path for an operation,
// qualifying both when another operation has already taken them. Two paths can
// carry the same operation ID (an alias route, or a collection that exists at
// more than one level), and the generated code would not compile if the name
// were emitted twice. The first operation to claim a name keeps it; later ones
// are qualified with the path-derived group, then with a counter. Paths are
// walked in sorted order, so which operation keeps the plain name is stable
// across runs. used is updated with the name returned.
func uniqueNames(name, group string, used map[string]bool) (string, string) {
	candidate := name

	if used[toGoName(candidate, true)] && group != "" {
		candidate = group + "-" + name
	}

	for i := 2; used[toGoName(candidate, true)]; i++ {
		candidate = name + "-" + strconv.Itoa(i)
	}

	used[toGoName(candidate, true)] = true

	return toGoName(candidate, true), slug(candidate)
}

func slug(operationID string) string {
	transformed := strings.ToLower(operationID)
	transformed = strings.Replace(transformed, "_", "-", -1)
	transformed = strings.Replace(transformed, " ", "-", -1)
	return transformed
}

func usage(name string, requiredParams []*Param) string {
	usage := slug(name)

	for _, p := range requiredParams {
		usage += " " + slug(p.Name)
	}

	return usage
}

func actionUsage(action string, requiredParams []*Param) string {
	u := slug(action)

	for _, p := range requiredParams {
		u += " " + slug(p.Name)
	}

	return u
}

func getParams(path *openapi3.PathItem, httpMethod string) []*Param {
	operation := path.Operations()[httpMethod]
	allParams := make([]*Param, 0, len(path.Parameters))

	var total openapi3.Parameters
	total = append(total, path.Parameters...)
	total = append(total, operation.Parameters...)

	for _, p := range total {
		if p.Value != nil && p.Value.Extensions["x-cli-ignore"] == nil {
			t := "string"
			tn := "\"\""
			if p.Value.Schema != nil && p.Value.Schema.Value != nil && p.Value.Schema.Value.Type != "" {
				switch p.Value.Schema.Value.Type {
				case "boolean":
					t = "bool"
					tn = "false"
				case "integer":
					t = "int64"
					tn = "0"
				case "number":
					t = "float64"
					tn = "0.0"
				}
			}

			cliName := slug(p.Value.Name)
			if p.Value.Extensions[ExtName] != nil {
				cliName = extStr(p.Value.Extensions[ExtName])
			}

			description := p.Value.Description
			if p.Value.Extensions[ExtDescription] != nil {
				description = extStr(p.Value.Extensions[ExtDescription])
			}

			allParams = append(allParams, &Param{
				Name:        p.Value.Name,
				CLIName:     cliName,
				GoName:      toGoName("param "+cliName, false),
				Description: escapeString(description),
				In:          p.Value.In,
				Required:    p.Value.Required,
				Type:        t,
				TypeNil:     tn,
			})
		}
	}

	return allParams
}

func getRequiredParams(allParams []*Param) []*Param {
	required := make([]*Param, 0)

	for _, param := range allParams {
		if param.Required || param.In == "path" {
			required = append(required, param)
		}
	}

	return required
}

func getOptionalParams(allParams []*Param) []*Param {
	optional := make([]*Param, 0)

	for _, param := range allParams {
		if !param.Required && param.In != "path" {
			optional = append(optional, param)
		}
	}

	return optional
}

func getRequestInfo(op *openapi3.Operation) (string, string, []interface{}) {
	mts := make(map[string][]interface{})

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		for mt, item := range op.RequestBody.Value.Content {
			var schema string
			var examples []interface{}

			if item.Schema != nil && item.Schema.Value != nil {
				// Let's make this a bit more concise. Since it has special JSON
				// marshalling functions, we do a dance to get it into plain JSON before
				// converting to YAML.
				data, err := json.Marshal(item.Schema.Value)
				if err != nil {
					continue
				}

				var unmarshalled interface{}
				json.Unmarshal(data, &unmarshalled)

				data, err = yaml.Marshal(unmarshalled)
				if err == nil {
					schema = string(data)
				}
			}

			if item.Example != nil {
				examples = append(examples, item.Example)
			} else {
				for _, ex := range item.Examples {
					if ex.Value != nil {
						examples = append(examples, ex.Value.Value)
						break
					}
				}
			}

			mts[mt] = []interface{}{schema, examples}
		}
	}

	// Prefer JSON.
	for mt, item := range mts {
		if strings.Contains(mt, "json") {
			return mt, item[0].(string), item[1].([]interface{})
		}
	}

	// Fall back to YAML next.
	for mt, item := range mts {
		if strings.Contains(mt, "yaml") {
			return mt, item[0].(string), item[1].([]interface{})
		}
	}

	// Last resort: return the first we find!
	for mt, item := range mts {
		return mt, item[0].(string), item[1].([]interface{})
	}

	return "", "", nil
}

func writeFormattedFile(filename string, data []byte) {
	formatted, errFormat := format.Source(data)
	if errFormat != nil {
		formatted = data
	}

	err := ioutil.WriteFile(filename, formatted, 0600)
	if errFormat != nil {
		panic(errFormat)
	} else if err != nil {
		panic(err)
	}
}

func initCmd(cmd *cobra.Command, args []string) {
	if _, err := os.Stat("main.go"); err == nil {
		fmt.Println("Refusing to overwrite existing main.go")
		return
	}

	data, _ := Asset("templates/main.tmpl")
	tmpl, err := template.New("cli").Parse(string(data))
	if err != nil {
		panic(err)
	}

	templateData := map[string]string{
		"Name":    args[0],
		"NameEnv": strings.Replace(strings.ToUpper(args[0]), "-", "_", -1),
	}

	var sb strings.Builder
	err = tmpl.Execute(&sb, templateData)
	if err != nil {
		panic(err)
	}

	writeFormattedFile("main.go", []byte(sb.String()))
}

func generate(cmd *cobra.Command, args []string) {
	data, err := ioutil.ReadFile(args[0])
	if err != nil {
		log.Fatal(err)
	}

	// Load the OpenAPI document.
	loader := openapi3.NewSwaggerLoader()
	var swagger *openapi3.Swagger
	swagger, err = loader.LoadSwaggerFromData(data)
	if err != nil {
		log.Fatal(err)
	}

	funcs := template.FuncMap{
		"escapeStr": escapeString,
		"slug":      slug,
		"title":     strings.Title,
	}

	data, _ = Asset("templates/commands.tmpl")
	tmpl, err := template.New("cli").Funcs(funcs).Parse(string(data))
	if err != nil {
		panic(err)
	}

	shortName := strings.TrimSuffix(path.Base(args[0]), ".yaml")

	templateData := ProcessAPI(shortName, swagger)

	var sb strings.Builder
	err = tmpl.Execute(&sb, templateData)
	if err != nil {
		panic(err)
	}

	writeFormattedFile(shortName+".go", []byte(sb.String()))
}

func main() {
	root := &cobra.Command{}

	root.AddCommand(&cobra.Command{
		Use:   "init <app-name>",
		Short: "Initialize and generate a `main.go` file for your project",
		Args:  cobra.ExactArgs(1),
		Run:   initCmd,
	})

	root.AddCommand(&cobra.Command{
		Use:   "generate <api-spec>",
		Short: "Generate a `commands.go` file from an OpenAPI spec",
		Args:  cobra.ExactArgs(1),
		Run:   generate,
	})

	root.Execute()
}
