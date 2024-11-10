package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "io/ioutil"
    "os"
    "strings"

    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/tools/clientcmd"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/rest"
    "gopkg.in/yaml.v2"
)

var (
    includeOptional     bool
    includeDescriptions bool
    includeConstraints  bool
    depth               int
    rawExample          bool
)

func main() {
    var crdName, filePath string

    flag.StringVar(&crdName, "crd-name", "", "Name of the CRD")
    flag.StringVar(&crdName, "n", "", "Name of the CRD")
    flag.StringVar(&filePath, "file", "", "Path to a file containing the CRD definition")
    flag.StringVar(&filePath, "f", "", "Path to a file containing the CRD definition")
    flag.BoolVar(&includeOptional, "include-optional", true, "Include optional fields in output")
    flag.BoolVar(&includeDescriptions, "include-descriptions", true, "Include field descriptions in output")
    flag.BoolVar(&includeConstraints, "include-constraints", true, "Include field constraints in output")
    flag.IntVar(&depth, "depth", 10, "Depth to extrapolate nested fields")
    flag.BoolVar(&rawExample, "raw-example", false, "If true, output all fields without comments, descriptions, or constraints")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: kubectl yamlgen [flags]\n\n")
        fmt.Fprintf(os.Stderr, "This command generates a templated yaml for any CRD\n\n")
        fmt.Fprintf(os.Stderr, "Flags:\n")
        fmt.Fprintf(os.Stderr, "  -n, --crd-name string    Name of the CRD in the cluster\n")
        fmt.Fprintf(os.Stderr, "  -f, --file               Path to a file containing the CRD definition\n")
	fmt.Fprintf(os.Stderr, "  --include-optional       Include optional fields in output (Default: true)\n")
        fmt.Fprintf(os.Stderr, "  --include-descriptions   Include field descriptions in output (Default: true)\n")
	fmt.Fprintf(os.Stderr, "  --include-constraints    Include field constraints in output (Default: true)\n")
	fmt.Fprintf(os.Stderr, "  --depth                  Depth to extrapolate nested fields (Default: 10)\n")
	fmt.Fprintf(os.Stderr, "  --raw-example            If true, output all fields without comments, descriptions, or constraints (Default: false)\n")
    }
    flag.Parse()
  
    if crdName == "" && filePath == "" {
        fmt.Println("Error: a CRD must be specified. use the --help flag for more details")
        os.Exit(1)
    }

    config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
    if err != nil {
        fmt.Printf("Error creating Kubernetes config: %v\n", err)
        os.Exit(1)
    }

    var crd *unstructured.Unstructured
    if crdName != "" {
        crd, err = fetchCRDFromCluster(config, crdName)
        if err != nil {
            fmt.Printf("Error fetching CRD: %v\n", err)
            os.Exit(1)
        }
    } else {
        crd, err = loadCRDFromFile(filePath)
        if err != nil {
            fmt.Printf("Error loading CRD from file: %v\n", err)
            os.Exit(1)
        }
    }

    storedVersion, schema, err := getStoredVersionAndSchema(crd)
    if err != nil {
        fmt.Println("Error:", err)
        os.Exit(1)
    }

    apiVersion := fmt.Sprintf("%s/%s", crd.Object["spec"].(map[string]interface{})["group"], storedVersion)
    kind := crd.Object["spec"].(map[string]interface{})["names"].(map[string]interface{})["kind"].(string)
    scope := crd.Object["spec"].(map[string]interface{})["scope"].(string)

    metadata := "  name: \"\"\n"
    if scope == "Namespaced" {
        metadata = "  name: \"\"\n  namespace: \"\"\n"
    }

    yamlOutput := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n%s", apiVersion, kind, metadata)
    
    // Add spec and other top-level fields
    yamlOutput += generateTopLevelYAML(schema, "spec", "  ", 1)

    fmt.Println(yamlOutput)
}

func generateTopLevelYAML(schema map[string]interface{}, fieldName string, indent string, currentDepth int) string {
    if currentDepth > depth {
        return ""
    }

    var yamlOutput strings.Builder

    if properties, found := schema["properties"].(map[string]interface{}); found {
        if field, exists := properties[fieldName]; exists {
            yamlOutput.WriteString(fmt.Sprintf("%s:\n", fieldName))
            yamlOutput.WriteString(generateYAMLFromSchema(field.(map[string]interface{}), fieldName, indent+"", currentDepth+1, false))
        }
    }

    return yamlOutput.String()
}

func generateYAMLFromSchema(schema map[string]interface{}, fieldName string, indent string, currentDepth int, isParentOptional bool) string {
    if currentDepth > depth {
        return ""
    }

    var yamlOutput strings.Builder
    properties, found := schema["properties"].(map[string]interface{})
    if !found {
        return ""
    }

    requiredFields := getRequiredFields(schema)

    for fieldName, fieldSchema := range properties {
        isRequired := requiredFields[fieldName]
        if !isRequired && !includeOptional && !rawExample {
            continue
        }

        fieldMap := fieldSchema.(map[string]interface{})
        commentPrefix := ""
        if !rawExample && (!isRequired || isParentOptional) {
            commentPrefix = "# "
        }

        // Add metadata comments with `#!` prefix only if there's actual content
        descriptionText := getString(fieldMap, "description")
        if descriptionText != "" && !rawExample && includeDescriptions {
            description := formatAsMultilineComment(fmt.Sprintf("Description: %s", descriptionText), 80, indent, "#!")
            yamlOutput.WriteString(description)
        }

        if !rawExample && includeConstraints {
            constraints := formatConstraints(fieldMap, indent, "#!")
            if constraints != "" {
                yamlOutput.WriteString(constraints)
            }
        }

        // Get the field type, including default value if available
        fieldType := getTypeWithDefault(fieldMap)
        if subProperties, found := fieldMap["properties"].(map[string]interface{}); found {
            yamlOutput.WriteString(fmt.Sprintf("%s%s%s:\n", indent, commentPrefix, fieldName))
            nestedSchema := map[string]interface{}{"properties": subProperties, "required": fieldMap["required"]}
            yamlOutput.WriteString(generateYAMLFromSchema(nestedSchema, fieldName, indent+"  ", currentDepth+1, !isRequired || isParentOptional))
        } else if items, found := fieldMap["items"].(map[string]interface{}); found {
            // Array item handling: Skip printing "object" and include only the fields
            yamlOutput.WriteString(fmt.Sprintf("%s%s%s:\n", indent, commentPrefix, fieldName))
            yamlOutput.WriteString(fmt.Sprintf("%s%s- \n", indent+"  ", commentPrefix)) // Array item base
            if subItems, ok := items["properties"].(map[string]interface{}); ok {
                nestedSchema := map[string]interface{}{"properties": subItems}
                yamlOutput.WriteString(generateYAMLFromSchema(nestedSchema, fieldName, indent+"    ", currentDepth+1, true))
            }
        } else {
            // Regular field with type
            yamlOutput.WriteString(fmt.Sprintf("%s%s%s: %s\n", indent, commentPrefix, fieldName, fieldType))
        }
    }

    return yamlOutput.String()
}

func formatAsMultilineComment(text string, lineWidth int, indent string, prefix string) string {
    if len(text) == 0 || rawExample {
        return ""
    }
    var result strings.Builder
    words := strings.Fields(text)
    line := fmt.Sprintf("%s%s ", indent, prefix)

    for _, word := range words {
        if len(line)+len(word)+1 > lineWidth {
            result.WriteString(line + "\n")
            line = fmt.Sprintf("%s%s ", indent, prefix)
        }
        line += word + " "
    }
    result.WriteString(line + "\n")
    return result.String()
}

func formatConstraints(fieldMap map[string]interface{}, indent string, prefix string) string {
    if rawExample {
        return ""
    }

    var constraints []string

    if enum, found := fieldMap["enum"]; found {
        constraints = append(constraints, fmt.Sprintf("Allowed values: %v", enum))
    }
    if maxLength, found := fieldMap["maxLength"]; found {
        constraints = append(constraints, fmt.Sprintf("Max length: %v", maxLength))
    }
    if minLength, found := fieldMap["minLength"]; found {
        constraints = append(constraints, fmt.Sprintf("Min length: %v", minLength))
    }
    if pattern, found := fieldMap["pattern"]; found {
        constraints = append(constraints, fmt.Sprintf("Pattern: %v", pattern))
    }
    if minimum, found := fieldMap["minimum"]; found {
        constraints = append(constraints, fmt.Sprintf("Minimum: %v", minimum))
    }
    if maximum, found := fieldMap["maximum"]; found {
        constraints = append(constraints, fmt.Sprintf("Maximum: %v", maximum))
    }
    if multipleOf, found := fieldMap["multipleOf"]; found {
        constraints = append(constraints, fmt.Sprintf("Multiple of: %v", multipleOf))
    }
    if maxItems, found := fieldMap["maxItems"]; found {
        constraints = append(constraints, fmt.Sprintf("Max items: %v", maxItems))
    }
    if minItems, found := fieldMap["minItems"]; found {
        constraints = append(constraints, fmt.Sprintf("Min items: %v", minItems))
    }
    if uniqueItems, found := fieldMap["uniqueItems"]; found && uniqueItems.(bool) {
        constraints = append(constraints, "Unique items required")
    }

    if len(constraints) == 0 {
        return ""
    }

    var formattedConstraints strings.Builder
    for _, constraint := range constraints {
        formattedConstraints.WriteString(fmt.Sprintf("%s%s Constraints: %s\n", indent, prefix, constraint))
    }
    return formattedConstraints.String()
}

func fetchCRDFromCluster(config *rest.Config, crdName string) (*unstructured.Unstructured, error) {
    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        return nil, err
    }
    gvr := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
    return dynamicClient.Resource(gvr).Get(context.TODO(), crdName, metav1.GetOptions{})
}

func convertMapKeysToString(m interface{}) interface{} {
    switch v := m.(type) {
    case map[interface{}]interface{}:
        newMap := make(map[string]interface{})
        for key, value := range v {
            strKey := fmt.Sprintf("%v", key)
            newMap[strKey] = convertMapKeysToString(value)
        }
        return newMap
    case map[string]interface{}:
        for key, value := range v {
            v[key] = convertMapKeysToString(value)
        }
        return v
    case []interface{}:
        for i, item := range v {
            v[i] = convertMapKeysToString(item)
        }
        return v
    case int:
        return int64(v) // Convert int to int64
    default:
        return v
    }
}

func loadCRDFromFile(filePath string) (*unstructured.Unstructured, error) {
    data, err := ioutil.ReadFile(filePath)
    if err != nil {
        return nil, err
    }

    var crd unstructured.Unstructured
    // Attempt to unmarshal as JSON first
    if jsonErr := json.Unmarshal(data, &crd.Object); jsonErr == nil {
        return &crd, nil
    }

    // If JSON unmarshalling fails, try YAML
    var yamlData map[interface{}]interface{}
    if yamlErr := yaml.Unmarshal(data, &yamlData); yamlErr == nil {
        crd.Object = convertMapKeysToString(yamlData).(map[string]interface{})
        return &crd, nil
    }

    return nil, fmt.Errorf("file is neither valid JSON nor YAML")
}
func getStoredVersionAndSchema(crd *unstructured.Unstructured) (string, map[string]interface{}, error) {
    versions, found, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
    if err != nil || !found {
        return "", nil, fmt.Errorf("CRD does not contain versions")
    }

    for _, version := range versions {
        versionMap, ok := version.(map[string]interface{})
        if !ok {
            continue
        }
        if storage, found := versionMap["storage"].(bool); found && storage {
            schema, found, err := unstructured.NestedMap(versionMap, "schema", "openAPIV3Schema")
            if found && err == nil {
                return versionMap["name"].(string), schema, nil
            }
        }
    }
    return "", nil, fmt.Errorf("stored version or openAPIV3Schema not found in any version of the CRD")
}

func getRequiredFields(schema map[string]interface{}) map[string]bool {
    requiredFields := map[string]bool{}
    if requiredList, found := schema["required"].([]interface{}); found {
        for _, field := range requiredList {
            if fieldName, ok := field.(string); ok {
                requiredFields[fieldName] = true
            }
        }
    }
    return requiredFields
}

func getString(m map[string]interface{}, key string) string {
    if val, ok := m[key].(string); ok {
        return val
    }
    return ""
}

// Updated to include default value if present
func getTypeWithDefault(fieldMap map[string]interface{}) string {
    fieldType := getType(fieldMap)
    if defaultValue, found := fieldMap["default"]; found {
        return fmt.Sprintf("%s (default: %v)", fieldType, defaultValue)
    }
    return fieldType
}

func getType(fieldMap map[string]interface{}) string {
    if fieldType, found := fieldMap["type"]; found {
        return fieldType.(string)
    }
    return "unknown"
}

