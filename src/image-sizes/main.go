package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "os/exec"
    "regexp"
    "sync"
    "path/filepath"
    "strings"

    "gopkg.in/yaml.v2"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodImageInfo stores image details for a container
type PodImageInfo struct {
    ContainerName string
    ImageURI      string
    Tag           string
    ShaDigest     string
    Size          string
    SizeBytes     int64
}

// PodImageReport contains report data for a single pod, including its namespace
type PodImageReport struct {
    PodName   string
    Namespace string
    Images    []PodImageInfo
}

var (
    kubeconfig    string
    contextName   string
    namespace     string
    allNamespaces bool
    outputFormat  string
    podName       string

    // Cache to store already inspected images
    imageCache = make(map[string]PodImageInfo)
    cacheMutex sync.Mutex
)

func init() {
    flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
    flag.StringVar(&contextName, "context", "", "Kubernetes context to use")
    flag.StringVar(&namespace, "namespace", "", "Namespace to use (defaults to current namespace)")
    flag.StringVar(&namespace, "n", "", "Namespace to use (shorthand for --namespace)")
    flag.BoolVar(&allNamespaces, "all-namespaces", false, "Query all namespaces (cannot be used with a pod name)")
    flag.BoolVar(&allNamespaces, "A", false, "Query all namespaces (shorthand for --all-namespaces)")
    flag.StringVar(&outputFormat, "output", "table", "Output format: table, json, yaml")
    flag.StringVar(&outputFormat, "o", "table", "Output format: table, json, yaml (shorthand for --output)")
    flag.StringVar(&podName, "pod", "", "Specific pod name to query")
    flag.StringVar(&podName, "p", "", "Specific pod name to query (shorthand for --pod)")
    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: kubectl image-sizes [flags]\n\n")
        fmt.Fprintf(os.Stderr, "This command outputs image sizes for containers per pod, namespace or cluster wide.\n\n")
        fmt.Fprintf(os.Stderr, "Flags:\n")
        fmt.Fprintf(os.Stderr, "  -A, --all-namespaces          Query all namespaces\n")
        fmt.Fprintf(os.Stderr, "  -n, --namespace <namespace>   Namespace to use\n")
        fmt.Fprintf(os.Stderr, "  -o, --output <format>         Output format: table, json, yaml\n")
        fmt.Fprintf(os.Stderr, "  -p, --pod <pod name>          Specific pod name to query\n")
        fmt.Fprintf(os.Stderr, "      --kubeconfig <file>       Path to the kubeconfig file\n")
        fmt.Fprintf(os.Stderr, "      --context <context>       Kubernetes context to use\n")
        os.Exit(1)
    }
}

func main() {
    flag.Parse()

    if allNamespaces && podName != "" {
        fmt.Println("Error: Cannot use --all-namespaces (-A) with a specific pod name.")
        os.Exit(1)
    }

    config, err := loadKubeConfig()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
        os.Exit(1)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating Kubernetes client: %v\n", err)
        os.Exit(1)
    }

    var reports []PodImageReport

    if podName != "" {
        report, err := getPodImageReport(clientset, namespace, podName)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Error retrieving pod data: %v\n", err)
            os.Exit(1)
        }
        reports = append(reports, report)
    } else {
        if allNamespaces {
            reports, err = getNamespaceImageReports(clientset) // Removed the string argument here
        } else {
            reports, err = getNamespaceImageReportsForSingleNamespace(clientset, namespace) // Separate function for single namespace
        }
        if err != nil {
            fmt.Fprintf(os.Stderr, "Error retrieving namespace data: %v\n", err)
            os.Exit(1)
        }
    }

    switch outputFormat {
    case "json":
        jsonOutput(reports)
    case "yaml":
        yamlOutput(reports)
    default:
        tableOutput(reports)
    }
}

func loadKubeConfig() (*rest.Config, error) {
    if kubeconfig == "" {
        kubeconfig = os.Getenv("KUBECONFIG")
    }
    if kubeconfig == "" {
        kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
    }

    configOverrides := &clientcmd.ConfigOverrides{}
    if contextName != "" {
        configOverrides.CurrentContext = contextName
    }

    loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
    return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
}

func getPodImageReport(clientset *kubernetes.Clientset, namespace, podName string) (PodImageReport, error) {
    pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
    if err != nil {
        return PodImageReport{}, err
    }

    report := PodImageReport{PodName: pod.Name, Namespace: pod.Namespace}
    totalContainers := len(pod.Spec.InitContainers) + len(pod.Spec.Containers)
    currentContainer := 0

    for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
        currentContainer++
        fmt.Printf("(%d/%d) Processing Container Image %s\n", currentContainer, totalContainers, container.Image)

        imageInfo, err := getImageDetails(container.Image, container.Name, pod, clientset) // Added clientset here
        if err != nil {
            return PodImageReport{}, fmt.Errorf("error retrieving image details for %s: %w", container.Image, err)
        }
        report.Images = append(report.Images, imageInfo)
    }
    return report, nil
}

func getImageDetails(imageURI, containerName string, pod *v1.Pod, clientset *kubernetes.Clientset) (PodImageInfo, error) {
    cleanedImage, tag, shaDigest := parseImageURI(imageURI)

    var fullImage string
    if shaDigest != "" {
        // If the image URI already includes a SHA digest, use it directly
	fullImage = cleanedImage + "@sha256:" + shaDigest
    } else {
        // For images with a tag, build the image reference with the tag
        fullImage = cleanedImage
        if tag != "" && tag != "N/A" {
            fullImage += ":" + tag
        }

        // Retrieve the node's architecture where the pod is running
        nodeName := pod.Spec.NodeName
        node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
        if err != nil {
            return PodImageInfo{}, fmt.Errorf("failed to get node information for pod %s: %v", pod.Name, err)
        }
        nodeArch := node.Status.NodeInfo.Architecture

        // Get the manifest list for the image
        cmd := exec.Command("docker", "manifest", "inspect", fullImage)
        output, err := cmd.CombinedOutput()
        if err != nil || !isMultiArch(output) {
            // Fallback: if it's not multi-arch, or if inspection fails, use the tag directly
            imageInfo, err := inspectSingleArchImage(fullImage, containerName, cleanedImage, tag)
            if err == nil {
                imageInfo.ShaDigest = extractShaDigest(fullImage, output) // Correctly extract SHA digest from output
            }
            return imageInfo, err
        }

        // Parse the manifest list to find the appropriate architecture if it’s multi-arch
        var manifestList struct {
            Manifests []struct {
                Platform struct {
                    Architecture string `json:"architecture"`
                } `json:"platform"`
                Digest string `json:"digest"`
            } `json:"manifests"`
        }
        err = json.Unmarshal(output, &manifestList)
        if err != nil {
            return PodImageInfo{}, fmt.Errorf("failed to parse manifest list for image %s: %v", fullImage, err)
        }

        var archDigest string
        for _, manifest := range manifestList.Manifests {
            if manifest.Platform.Architecture == nodeArch {
                archDigest = manifest.Digest
                break
            }
        }
        if archDigest == "" {
            return PodImageInfo{}, fmt.Errorf("no matching architecture (%s) found for image %s", nodeArch, fullImage)
        }

        // Use the architecture-specific SHA digest
        shaDigest = archDigest
        fullImage = cleanedImage + "@" + shaDigest
    }

    // Check cache to avoid repeated inspections
    cacheKey := fullImage
    cacheMutex.Lock()
    if cachedInfo, found := imageCache[cacheKey]; found {
        cacheMutex.Unlock()
        return cachedInfo, nil
    }
    cacheMutex.Unlock()

    // Perform the inspection and cache the result
    imageInfo, err := inspectSingleArchImage(fullImage, containerName, cleanedImage, tag)
    if err != nil {
        return PodImageInfo{}, err
    }

    // Only store the SHA digest, not the full URI in the output
    imageInfo.ShaDigest = shaDigest

    // Cache the inspected result
    cacheMutex.Lock()
    imageCache[cacheKey] = imageInfo
    cacheMutex.Unlock()

    return imageInfo, nil
}

// Helper function to extract only the SHA digest from a full image URI with SHA
func extractShaDigest(fullImage string, manifestOutput []byte) string {
    // Check if the fullImage contains a SHA reference
    if strings.Contains(fullImage, "@sha256:") {
        parts := strings.Split(fullImage, "@sha256:")
        if len(parts) == 2 {
            return "sha256:" + parts[1]
        }
    }
    // Fallback: Extract SHA from output if available
    manifest := struct {
        Digest string `json:"digest"`
    }{}
    _ = json.Unmarshal(manifestOutput, &manifest)
    return manifest.Digest
}

// Helper function to inspect single-architecture images
func inspectSingleArchImage(fullImage, containerName, cleanedImage, tag string) (PodImageInfo, error) {
    cmd := exec.Command("docker", "manifest", "inspect", fullImage)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return PodImageInfo{}, fmt.Errorf("docker manifest inspect failed for image %s: %s", fullImage, string(output))
    }

    var manifest struct {
        Layers []struct {
            Size int64 `json:"size"`
        } `json:"layers"`
    }
    err = json.Unmarshal(output, &manifest)
    if err != nil {
        return PodImageInfo{}, fmt.Errorf("failed to parse manifest for image %s: %v", fullImage, err)
    }

    totalSize := int64(0)
    for _, layer := range manifest.Layers {
        totalSize += layer.Size
    }

    return PodImageInfo{
        ContainerName: containerName,
        ImageURI:      cleanedImage,
        Tag:           tag,
        ShaDigest:     fullImage, // SHA if available
        Size:          formatSize(totalSize),
        SizeBytes:     totalSize,
    }, nil
}

// Helper function to determine if an image is multi-architecture
func isMultiArch(manifestOutput []byte) bool {
    var manifestCheck struct {
        Manifests []struct{} `json:"manifests"`
    }
    err := json.Unmarshal(manifestOutput, &manifestCheck)
    return err == nil && len(manifestCheck.Manifests) > 0
}

func getNamespaceImageReports(clientset *kubernetes.Clientset) ([]PodImageReport, error) {
    namespaceList, err := clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
    if err != nil {
        return nil, fmt.Errorf("error retrieving namespaces: %v", err)
    }
    totalNamespaces := len(namespaceList.Items)
    var allReports []PodImageReport

    for nsIndex, namespace := range namespaceList.Items {
        ns := namespace.Name
        fmt.Printf("Processing Namespace %d/%d: %s (%d%% Complete)\n", nsIndex+1, totalNamespaces, ns, (nsIndex+1)*100/totalNamespaces)

        reports, err := getNamespaceImageReportsForSingleNamespace(clientset, ns)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Warning: skipping namespace %s due to error: %v\n", ns, err)
            continue
        }
        allReports = append(allReports, reports...)
    }
    return allReports, nil
}

func getNamespaceImageReportsForSingleNamespace(clientset *kubernetes.Clientset, ns string) ([]PodImageReport, error) {
    pods, err := clientset.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
    if err != nil {
        return nil, fmt.Errorf("error retrieving pods for namespace %s: %v", ns, err)
    }

    var reports []PodImageReport
    totalPods := len(pods.Items)

    for i, pod := range pods.Items {
        percentComplete := (i + 1) * 100 / totalPods
        fmt.Printf("Processing Pod %d/%d in Namespace %s: %s (%d%% Complete)\n", i+1, totalPods, ns, pod.Name, percentComplete)

        report, err := getPodImageReport(clientset, pod.Namespace, pod.Name)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Warning: skipping pod %s in namespace %s due to error: %v\n", pod.Name, ns, err)
            continue
        }
        reports = append(reports, report)
    }
    return reports, nil
}

func parseImageURI(imageURI string) (string, string, string) {
    re := regexp.MustCompile(`^(.*?)(?::([^@]+))?(?:@sha256:(.+))?$`)
    matches := re.FindStringSubmatch(imageURI)

    cleanedImage := matches[1]
    tag := matches[2]
    shaDigest := matches[3]

    if tag == "" {
        tag = "N/A"
    }

    return cleanedImage, tag, shaDigest
}

func formatSize(sizeBytes int64) string {
    if sizeBytes < 1024*1024 {
        return fmt.Sprintf("%d KB", sizeBytes/1024)
    } else if sizeBytes < 1024*1024*1024 {
        return fmt.Sprintf("%d MB", sizeBytes/(1024*1024))
    }
    return fmt.Sprintf("%.2f GB", float64(sizeBytes)/(1024*1024*1024))
}

func jsonOutput(reports []PodImageReport) {
    jsonData, _ := json.MarshalIndent(reports, "", "  ")
    fmt.Println(string(jsonData))
}

func yamlOutput(reports []PodImageReport) {
    yamlData, _ := yaml.Marshal(reports)
    fmt.Println(string(yamlData))
}

func tableOutput(reports []PodImageReport) {
    // Initialize minimum column widths based on header names
    containerNameWidth := len("CONTAINER NAME")
    imageURIWidth := len("IMAGE URI")
    tagWidth := len("TAG")
    shaDigestWidth := len("SHA DIGEST")
    sizeWidth := len("SIZE")

    // Calculate the maximum width for each column based on data
    for _, report := range reports {
        for _, img := range report.Images {
            if len(img.ContainerName) > containerNameWidth {
                containerNameWidth = len(img.ContainerName)
            }
            if len(img.ImageURI) > imageURIWidth {
                imageURIWidth = len(img.ImageURI)
            }
            if len(img.Tag) > tagWidth {
                tagWidth = len(img.Tag)
            }
            if len(img.ShaDigest) > shaDigestWidth {
                shaDigestWidth = len(img.ShaDigest)
            }
            if len(img.Size) > sizeWidth {
                sizeWidth = len(img.Size)
            }
        }
    }

    // Print table for each pod with dynamically calculated widths
    for _, report := range reports {
        fmt.Printf("Pod: %s (Namespace: %s)\n", report.PodName, report.Namespace)
        fmt.Printf("%-*s %-*s %-*s %-*s %-*s\n",
            containerNameWidth, "CONTAINER NAME",
            imageURIWidth, "IMAGE URI",
            tagWidth, "TAG",
            shaDigestWidth, "SHA DIGEST",
            sizeWidth, "SIZE",
        )

        for _, img := range report.Images {
            fmt.Printf("%-*s %-*s %-*s %-*s %-*s\n",
                containerNameWidth, img.ContainerName,
                imageURIWidth, img.ImageURI,
                tagWidth, img.Tag,
                shaDigestWidth, img.ShaDigest,
                sizeWidth, img.Size,
            )
        }
        fmt.Println()
    }
}

