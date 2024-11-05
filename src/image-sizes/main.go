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

    "gopkg.in/yaml.v2"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"
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
        ns := namespace
        if allNamespaces {
            ns = metav1.NamespaceAll
        }
        reports, err = getNamespaceImageReports(clientset, ns)
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
    for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
        imageInfo, err := getImageDetails(container.Image, container.Name)
        if err != nil {
            return PodImageReport{}, fmt.Errorf("error retrieving image details for %s: %w", container.Image, err)
        }
        report.Images = append(report.Images, imageInfo)
    }
    return report, nil
}

func getNamespaceImageReports(clientset *kubernetes.Clientset, ns string) ([]PodImageReport, error) {
    pods, err := clientset.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
    if err != nil {
        return nil, err
    }

    var reports []PodImageReport
    for _, pod := range pods.Items {
        report, err := getPodImageReport(clientset, pod.Namespace, pod.Name)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Warning: skipping pod %s due to error: %v\n", pod.Name, err)
            continue
        }
        reports = append(reports, report)
    }
    return reports, nil
}

func getImageDetails(imageURI, containerName string) (PodImageInfo, error) {
    cleanedImage, tag, shaDigest := parseImageURI(imageURI)

    cacheKey := cleanedImage + ":" + tag + "@" + shaDigest
    cacheMutex.Lock()
    if cachedInfo, found := imageCache[cacheKey]; found {
        cacheMutex.Unlock()
        return cachedInfo, nil
    }
    cacheMutex.Unlock()

    fullImage := cleanedImage
    if shaDigest != "" {
        fullImage += "@sha256:" + shaDigest
    } else if tag != "" && tag != "N/A" {
        fullImage += ":" + tag
    }

    cmd := exec.Command("docker", "manifest", "inspect", fullImage)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return PodImageInfo{}, fmt.Errorf("docker manifest inspect failed for image %s: %s", imageURI, string(output))
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

    imageInfo := PodImageInfo{
        ContainerName: containerName,
        ImageURI:      cleanedImage,
        Tag:           tag,
        ShaDigest:     shaDigest,
        Size:          formatSize(totalSize),
        SizeBytes:     totalSize,
    }

    cacheMutex.Lock()
    imageCache[cacheKey] = imageInfo
    cacheMutex.Unlock()

    return imageInfo, nil
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
