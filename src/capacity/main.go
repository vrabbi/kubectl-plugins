package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "text/tabwriter"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    corev1 "k8s.io/api/core/v1"
    "sigs.k8s.io/yaml"
)

type NodeAllocation struct {
    NodeName            string `json:"node_name" yaml:"node_name"`
    PodCapacity       int64   `json:"pod_capacity,omitempty" yaml:"pod_capacity,omitempty"`
    DeployedPodCount  int64   `json:"deployed_pod_count,omitempty" yaml:"deployed_pod_count,omitempty"`
    AvailablePodSlots int64   `json:"available_pod_slots,omitempty" yaml:"available_pod_slots,omitempty"`
    CPUCapacity       float64 `json:"cpu_capacity,omitempty" yaml:"cpu_capacity,omitempty"`
    CPUAllocated      float64 `json:"cpu_allocated,omitempty" yaml:"cpu_allocated,omitempty"`
    CPUAvailable      float64 `json:"cpu_available,omitempty" yaml:"cpu_available,omitempty"`
    RAMCapacity       float64 `json:"ram_capacity,omitempty" yaml:"ram_capacity,omitempty"`
    RAMAllocated      float64 `json:"ram_allocated,omitempty" yaml:"ram_allocated,omitempty"`
    RAMAvailable      float64 `json:"ram_available,omitempty" yaml:"ram_available,omitempty"`
}

func main() {
    // Command-line flags
    kubeconfig := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
    contextName := flag.String("context", "", "name of the kubeconfig context to use")
    outputFormat := flag.String("output", "table", "output format: table, json, yaml (use -o for short form)")
    noHeaders := flag.Bool("no-headers", false, "if true, omit header row in output")
    selector := flag.String("selector", "", "label selector to filter nodes")
    cpuOnly := flag.Bool("cpu-only", false, "if true, show only CPU data")
    ramOnly := flag.Bool("ram-only", false, "if true, show only RAM data")
    podsOnly := flag.Bool("pods-only", false, "if true, show only pod data")

    // Short output flag
    outputFlag := flag.String("o", "table", "output format: table, json, yaml")
    selectorFlag := flag.String("l", "", "label selector to filter nodes")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: kubectl pod-capacity [flags]\n\n")
	fmt.Fprintf(os.Stderr, "This command outputs resource usage and capacity data for nodes in your cluster. It supports exposing pod, cpu and ram data\n\n")
        fmt.Fprintf(os.Stderr, "Flags:\n")
        fmt.Fprintf(os.Stderr, "  -o, --output string      output format: table, json, yaml\n")
        fmt.Fprintf(os.Stderr, "  --kubeconfig string      absolute path to the kubeconfig file\n")
        fmt.Fprintf(os.Stderr, "  --context string         name of the kubeconfig context to use\n")
        fmt.Fprintf(os.Stderr, "  --no-headers             if true, omit header row in output\n")
        fmt.Fprintf(os.Stderr, "  -l, --selector string    label selector to filter nodes\n")
	fmt.Fprintf(os.Stderr, " --cpu-only                if true, show only CPU data\n")
	fmt.Fprintf(os.Stderr, " --ram-only                if true, show only RAM data\n")
	fmt.Fprintf(os.Stderr, " --pods-only               if true, show only pod data\n")
    }

    flag.Parse()

    // Determine the output format
    if *outputFlag != "table" { // If -o is specified, it takes precedence over --output
        *outputFormat = *outputFlag
    }
    if *selectorFlag != "" { 
        *selector = *selectorFlag
    }

    // Load kubeconfig
    if *kubeconfig == "" {
	if kubeconfigEnv, exists := os.LookupEnv("KUBECONFIG"); exists {
            *kubeconfig = kubeconfigEnv
        } else {
            homeDir, err := os.UserHomeDir()
            if err != nil {
                fmt.Printf("Error getting home directory: %s\n", err.Error())
                os.Exit(1)
            }
            *kubeconfig = filepath.Join(homeDir, ".kube", "config")
        }
    }

    config, err := clientcmd.BuildConfigFromFlags(*contextName, *kubeconfig)
    if err != nil {
        fmt.Printf("Error building kubeconfig: %s\n", err.Error())
        os.Exit(1)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        fmt.Printf("Error creating Kubernetes client: %s\n", err.Error())
        os.Exit(1)
    }

    // Fetch nodes with optional label selector
    nodeListOptions := metav1.ListOptions{}
    if *selector != "" {
        nodeListOptions.LabelSelector = *selector
    }

    nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), nodeListOptions)
    if err != nil {
        fmt.Printf("Error fetching nodes: %s\n", err.Error())
        os.Exit(1)
    }

    var allocations []NodeAllocation
    for _, node := range nodes.Items {
    nodeName := node.Name

    // Get resource quantities
    podCapacity := node.Status.Capacity[corev1.ResourcePods]
    cpuCapacity := node.Status.Capacity[corev1.ResourceCPU]
    ramCapacity := node.Status.Capacity[corev1.ResourceMemory]
    // Other calculations
    deployedPodCount := getPodCountForNode(clientset, nodeName)
    cpuAllocated := getCPUAllocatedForNode(clientset, nodeName)
    ramAllocated := getRAMAllocatedForNode(clientset, nodeName)

    // Append to allocations
    alloc := NodeAllocation{NodeName: nodeName}
        if !*cpuOnly && !*ramOnly && !*podsOnly {
            // Include all data if no specific flag is set
            alloc.PodCapacity = podCapacity.Value()
            alloc.DeployedPodCount = deployedPodCount
            alloc.AvailablePodSlots = podCapacity.Value() - deployedPodCount
            alloc.CPUCapacity = float64(cpuCapacity.MilliValue()) / 1000.0
            alloc.CPUAllocated = float64(cpuAllocated) / 1000.0
            alloc.RAMCapacity = float64(ramCapacity.Value()) / (1024 * 1024 * 1024)
            alloc.RAMAllocated = float64(ramAllocated) / (1024 * 1024 * 1024)
            alloc.CPUAvailable = float64(cpuCapacity.MilliValue()-cpuAllocated) / 1000.0
            alloc.RAMAvailable = float64(ramCapacity.Value()-ramAllocated) / (1024 * 1024 * 1024)
        } else {
            if *cpuOnly {
                alloc.CPUCapacity = float64(cpuCapacity.MilliValue()) / 1000.0
                alloc.CPUAllocated = float64(cpuAllocated) / 1000.0
                alloc.CPUAvailable = float64(cpuCapacity.MilliValue()-cpuAllocated) / 1000.0
            }
            if *ramOnly {
                alloc.RAMCapacity = float64(ramCapacity.Value()) / (1024 * 1024 * 1024)
                alloc.RAMAllocated = float64(ramAllocated) / (1024 * 1024 * 1024)
                alloc.RAMAvailable = float64(ramCapacity.Value()-ramAllocated) / (1024 * 1024 * 1024)
            }
            if *podsOnly {
                alloc.PodCapacity = podCapacity.Value()
                alloc.DeployedPodCount = deployedPodCount
                alloc.AvailablePodSlots = podCapacity.Value() - deployedPodCount
            }
        }
        allocations = append(allocations, alloc)
}
    // Output based on the specified format
    switch *outputFormat {
    case "json":
        outputJSON(allocations)
    case "yaml":
        outputYAML(allocations)
    case "table":
	outputTable(allocations, *noHeaders, *cpuOnly, *ramOnly, *podsOnly)
    default:
        fmt.Println("Invalid output format. Supported formats: table, json, yaml.")
        os.Exit(1)
    }
}

func getPodCountForNode(clientset *kubernetes.Clientset, nodeName string) int64 {
    pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
        FieldSelector: "spec.nodeName=" + nodeName,
    })
    if err != nil {
        fmt.Printf("Error fetching pods for node %s: %s\n", nodeName, err.Error())
        return 0
    }
    return int64(len(pods.Items))
}
// Function to get allocated CPU
func getCPUAllocatedForNode(clientset *kubernetes.Clientset, nodeName string) int64 {
    pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
        FieldSelector: "spec.nodeName=" + nodeName,
    })
    if err != nil {
        fmt.Printf("Error fetching pods for node %s: %s\n", nodeName, err.Error())
        return 0
    }

    var totalCPUAllocated int64
    for _, pod := range pods.Items {
        for _, container := range pod.Spec.Containers {
            if cpuRequest, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
                totalCPUAllocated += cpuRequest.MilliValue() // Correct usage
            }
        }
    }
    return totalCPUAllocated
}

// Function to get allocated RAM
func getRAMAllocatedForNode(clientset *kubernetes.Clientset, nodeName string) int64 {
    pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
        FieldSelector: "spec.nodeName=" + nodeName,
    })
    if err != nil {
        fmt.Printf("Error fetching pods for node %s: %s\n", nodeName, err.Error())
        return 0
    }

    var totalRAMAllocated int64
    for _, pod := range pods.Items {
        for _, container := range pod.Spec.Containers {
            if ramRequest, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
                totalRAMAllocated += ramRequest.Value() // Correct usage
            }
        }
    }
    return totalRAMAllocated
}
func outputJSON(allocations []NodeAllocation) {
    data, err := json.MarshalIndent(allocations, "", "  ")
    if err != nil {
        fmt.Printf("Error marshaling JSON: %s\n", err.Error())
        os.Exit(1)
    }
    fmt.Println(string(data))
}

func outputYAML(allocations []NodeAllocation) {
    data, err := yaml.Marshal(allocations)
    if err != nil {
        fmt.Printf("Error marshaling YAML: %s\n", err.Error())
        os.Exit(1)
    }
    fmt.Println(string(data))
}

func outputTable(allocations []NodeAllocation, noHeaders, cpuOnly, ramOnly, podsOnly bool) {
    w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
    if !noHeaders {
        if cpuOnly {
            fmt.Fprintf(w, "%-30s\t%-15s\t%-15s\t%-15s\n", "NODE_NAME", "CPU CAPACITY (Cores)", "CPU ALLOCATED (Cores)", "CPU AVAILABLE (Cores)")
        } else if ramOnly {
            fmt.Fprintf(w, "%-30s\t%-15s\t%-15s\t%-15s\n", "NODE_NAME", "RAM CAPACITY (GB)", "RAM ALLOCATED (GB)", "RAM AVAILABLE (GB)")
        } else if podsOnly {
            fmt.Fprintf(w, "%-30s\t%-15s\t%-20s\t%-20s\n", "NODE_NAME", "POD CAPACITY", "DEPLOYED POD COUNT", "AVAILABLE POD SLOTS")
        } else {
            fmt.Fprintf(w, "%-30s\t%-15s\t%-20s\t%-20s\t%-15s\t%-15s\t%-15s\t%-15s\t%-15s\t%-15s\n",
                "NODE_NAME", "POD CAPACITY", "DEPLOYED POD COUNT", "AVAILABLE POD SLOTS",
                "CPU CAPACITY (Cores)", "CPU ALLOCATED (Cores)", "CPU AVAILABLE (Cores)",
                "RAM CAPACITY (GB)", "RAM ALLOCATED (GB)", "RAM AVAILABLE (GB)")
        }
    }
    for _, alloc := range allocations {
        if cpuOnly {
            fmt.Fprintf(w, "%-30s\t%-15.2f\t%-15.2f\t%-15.2f\n", alloc.NodeName, alloc.CPUCapacity, alloc.CPUAllocated, alloc.CPUAvailable)
        } else if ramOnly {
            fmt.Fprintf(w, "%-30s\t%-15.2f\t%-15.2f\t%-15.2f\n", alloc.NodeName, alloc.RAMCapacity, alloc.RAMAllocated, alloc.RAMAvailable)
        } else if podsOnly {
            fmt.Fprintf(w, "%-30s\t%-15d\t%-20d\t%-20d\n", alloc.NodeName, alloc.PodCapacity, alloc.DeployedPodCount, alloc.AvailablePodSlots)
        } else {
            fmt.Fprintf(w, "%-30s\t%-15d\t%-20d\t%-20d\t%-15.2f\t%-15.2f\t%-15.2f\t%-15.2f\t%-15.2f\t%-15.2f\n",
                alloc.NodeName, alloc.PodCapacity, alloc.DeployedPodCount,
                alloc.AvailablePodSlots, alloc.CPUCapacity, alloc.CPUAllocated, alloc.CPUAvailable,
                alloc.RAMCapacity, alloc.RAMAllocated, alloc.RAMAvailable)
        }
    }
    w.Flush()
}
