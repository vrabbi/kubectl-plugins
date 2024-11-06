#!/bin/bash

set -e

# Check input arguments
if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <plugin-name>"
  exit 1
fi

mkdir src/$1

cd src/$1
go mod init $1
cat <<EOF > main.go
package main

import (
    "flag"
    "fmt"
    "os"
    "path/filepath"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
)

var (
    kubeconfig    string
    contextName   string
    namespace     string
    allNamespaces bool
    outputFormat  string
)

func main() {
    // Command-line flags
    flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
    flag.StringVar(&contextName, "context", "", "Kubernetes context to use")
    flag.StringVar(&namespace, "namespace", "", "Namespace to use (defaults to current namespace)")
    flag.StringVar(&namespace, "n", "", "Namespace to use (shorthand for --namespace)")
    flag.BoolVar(&allNamespaces, "all-namespaces", false, "Query all namespaces (cannot be used with a pod name)")
    flag.BoolVar(&allNamespaces, "A", false, "Query all namespaces (shorthand for --all-namespaces)")
    flag.StringVar(&outputFormat, "output", "table", "Output format: table, json, yaml")
    flag.StringVar(&outputFormat, "o", "table", "Output format: table, json, yaml (shorthand for --output)")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: kubectl ${1} [flags]\n\n")
        fmt.Fprintf(os.Stderr, "This command ... \n\n")
        fmt.Fprintf(os.Stderr, "Flags:\n")
        fmt.Fprintf(os.Stderr, "  -A, --all-namespaces          Query all namespaces\n")
        fmt.Fprintf(os.Stderr, "  -n, --namespace <namespace>   Namespace to use\n")
        fmt.Fprintf(os.Stderr, "  -o, --output string      output format: table, json, yaml\n")
        fmt.Fprintf(os.Stderr, "  --kubeconfig string      absolute path to the kubeconfig file\n")
        fmt.Fprintf(os.Stderr, "  --context string         name of the kubeconfig context to use\n")
    }

    flag.Parse()

    // Load kubeconfig
    if kubeconfig == "" {
        if kubeconfigEnv, exists := os.LookupEnv("KUBECONFIG"); exists {
            kubeconfig = kubeconfigEnv
        } else {
            homeDir, err := os.UserHomeDir()
            if err != nil {
                fmt.Printf("Error getting home directory: %s\n", err.Error())
                os.Exit(1)
            }
            kubeconfig = filepath.Join(homeDir, ".kube", "config")
        }
    }

    config, err := clientcmd.BuildConfigFromFlags(contextName, kubeconfig)
    if err != nil {
        fmt.Printf("Error building kubeconfig: %s\n", err.Error())
        os.Exit(1)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        fmt.Printf("Error creating Kubernetes client: %s\n", err.Error())
        os.Exit(1)
    }
    _ = clientset
}
EOF
go mod tidy
