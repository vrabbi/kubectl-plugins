# Kubectl Plugin Repo
This repo contains the source code for custom kubectl plugins as well as a krew plugin index folder which includes many of the most common and useful upstream plugins.

To add this custom index on your machine once you have krew installed simply run:
```bash
kubectl krew index add vrabbi https://github.com/vrabbi/kubectl-plugins.git
```

# Adding a new upstream plugin to the plugin index
To sync upstream plugins we are utilizing carvel vendir.

1. Simply add the sub-path within the https://github.com/kubernetes-sigs/krew-index repo under includePaths within the vendir.yml config file
2. run `vendir sync`
3. run `cp upstream/* plugins/`
4. push the changes to git

# Starting a new plugin in this repo
To start working on a new plugin:
1. Run the new-plugin.sh script as follows:
   ```bash
   ./new-plugin.sh <PLUGIN NAME>
   ```
2. Go to the new source code directory
   ```bash
   cd src/<PLUGIN NAME>
   ```
3. Add your custom logic to the main.go file

# Releasing a new version of a plugin
To release a new version of a plugin:
1. run the release.sh script as follows with the version being a valid semver prefixed with a v (e.g. v0.1.0)
   ```bash
   ./release.sh <PLUGIN NAME> <VERSION>
   ```
2. validate a new GitHub Release was created and that the manifest under the plugins directory called <PLUGIN NAME>.yaml is updated with the new artifact URLs and version
3. to install from the index simply update your local cache with:
   ```bash
   kubectl krew update
   ```
