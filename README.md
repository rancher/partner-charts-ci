# partner-charts-ci

`partner-charts-ci` is used to automate many parts of working with the
[Rancher Partner Charts Repository](https://github.com/rancher/partner-charts).
More specifically, it is for taking config that specifies "upstream" helm
charts, downloading those charts, modifying them for Rancher, and using them
to construct a new helm chart repository. It also includes commands for things
like validating the state of the repository and removing out-of-date versions
of helm charts.


## The Basics

### Terminology

A **package** is a set of configuration and files that specifies an upstream
helm chart and modifications applied to it when integrating new versions of it
into the repository. There is a single package for every chart in the repository.

A **chart** is a helm chart.

A **chart version** is a specific version of a chart. Each chart included in
the repository has one or more chat versions.

Generally speaking, **upstream** refers to the place a chart comes from. It
can take the form of a helm repository, an Artifact Hub repository or a git
repository.

### Directory Structure

`partner-charts-ci` expects certain files and directories to exist when operating on
a repository:

`configuration.yaml`: contains `partner-charts-ci` configuration.

`packages/`: contains package directories. Package directories are of the form
`packages/<vendor>/<name>/`, where `vendor` is the vendor that provides the chart
and `name` is the name of the chart. Taken together, `<vendor>/<name>` is the full
name used to refer to the package.

`charts/`: contains an unarchived version of every chart version in the repository.

`assets/`: contains all chart versions in .tgz format. This is the authority on
repository state.


## Workflow

#### 1. Fork the [Rancher Partner Charts](https://github.com/rancher/partner-charts/) repository, clone your fork, checkout the **main-source** branch and pull the latest changes.
Then create a new branch off of main-source

#### 2. Create subdirectories in **packages** in the form of `<vendor>/<chart>`
```bash
cd partner-charts
mkdir -p packages/suse/kubewarden-controller

```
#### 3. Create your [upstream.yaml](#configuration-file) in `packages/<vendor>/<chart>`

The tool reads a configuration yaml, `upstream.yaml`, to know where to fetch the upstream chart. This file is also able to define any alterations for valid variables in the Chart.yaml as described by [Helm](https://helm.sh/docs/topics/charts/#the-chartyaml-file).

**Important:** In GKE clusters, a Helm Chart will NOT display in Rancher Apps unless `kubeVersion` includes `-0` suffix in `Chart.yaml` For example:
```bash
kubeVersion: '>= 1.19.0-0'
```
Some [example upstream.yaml](#examples) are provided below
```bash
cat <<EOF > packages/suse/kubewarden-controller/upstream.yaml
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
EOF
```
#### 4. [Create 'overlay' files](#overlay)
Create any add-on files such as an `app-readme.md` and `questions.yml` in an `overlay` subdirectory (Optional)
```bash
mkdir packages/suse/kubewarden-controller/overlay
echo "Example app-readme.md" > packages/suse/kubewarden-controller/overlay/app-readme.md
```
#### 5. Commit your packages directory
```bash
git add packages/suse/kubewarden-controller
git commit -m "Submitting suse/kubewarden-controller"
```
#### 6. [Test your configuration](#testing-your-configuration)
#### 7. Push your commit
```bash
git push origin <your_branch>
```
#### 8. Open a pull request to **main-source** branch


## Testing your configuration

If you would like to test your configuration, download `partner-charts-ci`
using `scripts/pull-scripts`. The `update` function can be used to download and
integrate your chart.

#### 1. Download `partner-charts-ci`
```bash
scripts/pull-scripts
```
#### 2. Set the **PACKAGE** environment variable to your chart
You can confirm the package entry with `bin/partner-charts-ci list` which will list all detected charts with a configuration file.
```bash
export PACKAGE=<vendor>/<chart>
```
#### 3. Run the `update` subcommand
The `update` subcommand will go through the CI process. Append the `--commit`
flag if you want it to create a git commit when it completes.
```bash
bin/partner-charts-ci update --commit
```
#### 4. Validate your changes
```bash
bin/partner-charts-ci validate
```
#### Testing new chart on Rancher Apps UI
1. If you haven't done so yet, pull down your new chart files into your local `partner-charts` repository:
```bash
a) Get scripts: scripts/pull-scripts
b) List and find your company name/chart: bin/partner-charts-ci list | grep <vendor>
c) set PACKAGE variable to your company/chart: export PACKAGE=<vendor>/<chart-name> or export PACKAGE=<vendor>
d) Run bin/partner-charts-ci update # the new charts should be downloaded
```
2.  In your local `partner-charts` directory start a python3 http server:
```bash
#python3 -m http.server 8000
```
3. From a second terminal expose your local http server via ngrok ( https://ngrok.com/download )
```bash
#./ngrok http 8000
```
4. In Rancher UI create a test repository that points to your local `partner-charts` repo by selecting an appropriate cluster and going to Apps > Repositories and clicking "Create".  Enter a Name, copy ngrok forwarding url and paste it into Target http(s) "Index URL" and click "Create" again.

5. Once the new repository is "Active" go to Apps > Charts , find your new chart, review Readme is correct, etc. and install it. It should be successfully deployed.

## Overlay

Any files placed in the `packages/<vendor>/<chart>/overlay` directory will be overlayed onto the chart. This allows for adding or overwriting files within the chart as needed. The primary intended purpose is for adding the optional app-readme.md and questions.yml files but it may be used for adding or replacing any chart files.

- `app-readme.md` - Write a brief description of the app and how to use it. It's recommended to keep
it short as the longer `README.md` in your chart will be displayed in the UI as detailed description.

- `questions.yml` - Defines a set of questions to display in the chart's installation page in order for users to
answer them and configure the chart using the UI instead of modifying the chart's values file directly.

#### Questions example
[Variable Reference](https://docs.ranchermanager.rancher.io/how-to-guides/new-user-guides/helm-charts-in-rancher/create-apps#question-variable-reference)
```yaml
questions:
- variable: password
  default: ""
  required: true
  type: password
  label: Admin Password
  group: "Global Settings"
- variable: service.type
  default: "ClusterIP"
  type: enum
  group: "Service Settings"
  options:
    - "ClusterIP"
    - "NodePort"
    - "LoadBalancer"
  required: true
  label: Service Type
  show_subquestion_if: "NodePort"
  subquestions:
  - variable: service.nodePort
    default: ""
    description: "NodePort port number (to set explicitly, choose port between 30000-32767)"
    type: int
    min: 30000
    max: 32767
    label: Service NodePort
```

## Configuration File

Options for `upstream.yaml`
| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| AutoInstall | | Allows setting a required additional chart to deploy prior to current chart, such as a dedicated CRDs chart
| ChartMetadata | | Allows setting/overriding the value of any valid [Chart.yaml variable](https://helm.sh/docs/topics/charts/#the-chartyaml-file)
| Deprecated | | Whether the package is deprecated. Deprecated packages will not integrate any new chart versions from upstream. Do not set this field directly; instead, use `partner-charts-ci deprecate`.
| DisplayName | | Sets the name the chart will be listed under in the Rancher UI
| Experimental | | Adds the 'experimental' annotation which adds a flag on the UI entry
| Fetch | HelmChart, HelmRepo | Selects set of charts to pull from upstream.<br />- **latest** will pull only the latest chart version *default*<br />- **newer** will pull all newer versions than currently stored<br />- **all** will pull all versions
| GitBranch | GitRepo | Defines which branch to pull from the upstream GitRepo
| GitHubRelease | GitRepo | If true, will pull latest GitHub release from repo. Requires GitHub URL
| GitRepo | | Defines the git repo to pull from
| GitSubdirectory | GitRepo | Allows selection of a subdirectory of the upstream git repo to pull the chart from
| HelmChart | HelmRepo | Defines which chart to pull from the upstream Helm repo
| HelmRepo | HelmChart | Defines the upstream Helm repo to pull from
| Hidden | | Adds the 'hidden' annotation which hides the chart from the Rancher UI. Do not set this field directly unless the package is new; instead, use `partner-charts-ci hide`.
| Namespace | | Addes the 'namespace' annotation which hard-codes a deployment namespace for the chart
| PackageVersion | | Used to generate new patch version of chart
| ReleaseName | | Sets the value of the release-name Rancher annotation. Defaults to the chart name
| Vendor | | Sets the vendor name providing the chart

## Examples
### Helm Repo
#### Minimal Requirements
```yaml
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
```
#### Multiple Release Streams
```yaml
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
Fetch: newer
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Artifact Hub
```yaml
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Git Repo
```yaml
GitRepo: https://github.com/kubewarden/helm-charts.git
GitBranch: main
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### GitHub Release
```yaml
GitRepo: https://github.com/kubewarden/helm-charts.git
GitHubRelease: true
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```
## Migrate existing chart to automated updates
These steps are for charts still using `package.yaml` to track upstream chart.  These charts should be migrated to receive automatic updates via an `upstream.yaml` by following the steps below.  After chart is migrated, it should get updated from your helm/github repo automatically.

#### 1. Fork partner-charts repository, clone your fork, checkout the main-source branch and pull the latest changes. Then create a new branch off of main-source

#### 2. Create directory structure for your company and chart in `packages/<vendor>/<chart>` e.g.
```bash
mkdir -p partner-charts/packages/suse/kubewarden-controller
```
#### 3. Create an `upstream.yaml` in `packages/<vendor>/<chart>`
If your existing chart is using a high patch version like 5.5.100 due to old method of taking version 5.5.1 and modifying it with the PackageVersion, add `PackageVersion` to the `upstream.yaml` (set it to 01 , 00 is not valid). Ideally, when the the next minor version is released e.g. 5.6.X you can then remove `PackageVersion` from the `upstream.yaml` since 5.6.X > 5.5.XXX.  E.g.
```yaml
cat <<EOF > packages/suse/kubewarden-controller/upstream.yaml
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
PackageVersion: 01 # add if existing chart is using high patch version
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
EOF
```
#### 4. If there is an `overlay` dir in `partner-charts/packages/<chart>/generated-changes/` move it to `packages/<vendor>/<chart>/` and ensure only necessary files are present in overlay dir e.g.
```bash
mv partner-charts/packages/kubewarden-controller/generated-changes/overlay partner-charts/packages/suse/kubewarden-controller/
```
Check the old generated-changes/patch directory for any requisite other changes. If there is an edit in `Chart.yaml.patch` that needs to be replicated, it can be handled in the `upstream.yaml` `ChartMetadata` (see https://github.com/rancher/partner-charts#configuration-file).  If it is a change for any other file in the chart it can be done via an overlay file. See https://github.com/rancher/partner-charts#overlay

#### 5. Clean up old packages and charts directories:
```bash
git rm -r packages/<chart>
git rm -r charts/<chart>
```
* Note: If a chart is using a logo file in partner-charts repo, make sure the `icon:` variable is set correctly in the `upstream.yaml ChartMetadata`.

#### 6. Stage your changes (To make sure the config works, and to setup the new charts and assets directories)
```bash
export PACKAGE=<vendor>/<chart>
bin/partner-charts-ci update
```
#### 7. Move the old assets files to the new directory (Sometimes this is unchanged but most times it does change)
```bash
git mv assets/<chart>/* assets/<vendor>/
```
#### 8. Update the `index.yaml` to reflect the new assets path for existing entries
```bash
sed -i 's%assets/<chart>%assets/<vendor>%' index.yaml
```
After doing this,  run this loop to validate that every assets file referenced in the index actually exists, it makes sure your paths aren't edited incorrectly.
```bash
for charts in $(yq '.entries[][] | .urls[0]' index.yaml); do stat ${charts} > /dev/null; if [[ ! $? -eq 0 ]]; then echo ${charts}; fi; done
```
The command should return quickly with no output. If it outputs anything it means some referenced assets files don't exist which is a problem.
#### 9. Add/Commit your changes
```bash
git add assets charts packages index.yaml
git commit -m "Migrating <vendor> <chart> chart"
```
#### 10. Push your commit
```bash
git push origin <your branch>
```
#### 11. Open a pull request  to the `main-source` branch for review








## Building

Binaries are provided for macOS (Universal) and Linux (x86_64).

Ensure your host has Golang 1.18 or newer then simply build with
```bash
make build
```


#### macOS Universal Build

```bash
make build-darwin-universal
```

## CI Process

The majority of the day-to-day CI operation is handled by the 'auto' subcommand
which will run a full check against all configured charts, download any updates,
and form a commit with the changes.

#### 1. Clone your fork of the [Rancher Partner Charts](github.com/rancher/partner-charts) repository

```bash
git clone -b main-source git@github.com:<your_github>/partner-charts.git
````

#### 2. Ensure that `git status` is reporting a clean working tree

```bash
➜  partner-charts git:(main-source) git status
On branch main-source
Your branch is up to date with 'origin/main-source'.

nothing to commit, working tree clean
```

#### 3. Pull the latest CI build

```bash
scripts/pull-ci-scripts
```

#### 4. Run the auto function

```bash
bin/partner-charts-ci auto
```

#### 5. Run a validation

```bash
bin/partner-charts-ci validate
```

#### 6. Checkout the 'main' branch

```bash
git checkout main
```

#### 7. Remove the current `index.yaml` and `assets`

```bash
rm -r assets index.yaml
```

#### 8. Copy in the updated `index.yaml` and `assets`

```bash
git checkout main-source -- index.yaml assets
```

#### 9. Add, commit, and push your changes

```bash
git add index.yaml assets
git commit -m "Release Partner Charts"
git push origin main
git push origin main-source
```

#### 10. Open a Pull-Request for both your `main-source` and `main` branches

- The `main-source` PR message should auto-populate with the list of additions/updates
- For the `main` PR you should include the PR number for the related `main-source` PR

## Featuring or Hiding a chart

Featuring and hiding charts is done by appending the `catalog.cattle.io/
featured` or `catalog.cattle.io/hidden` chart annotation, respectively.  The CI
tool is able to perform these changes for you, to easly update the asset gzip,
the charts directory, and the index.yaml.

If you open a PR after modifying an existing chart, the `validation` stage will
expectedly fail, as the main goal is to ensure no accidental modification of
already released charts.

In order to avoid this, somewhere in the title of the PR, include the string
`[modified charts]`. This will cause the PR check to skip that part of the
validation. For example, when you open the PR you could title it "Hiding suse/
kubewarden-controller chart [modified charts]".

To view the currently featured charts:
```bash
bin/partner-charts-ci feature list
```
To feature a chart:
```bash
bin/partner-charts-ci feature add suse/kubewarden-controller 2
```
To remove the featured annotation:
```bash
bin/partner-charts-ci feature remove suse/kubewarden-controller
```
To hide the chart:
```bash
bin/partner-charts-ci hide suse/kubewarden-controller
```

After any of those changes, simply add, commit, push - and open a PR with
"[modified charts]" in the title:
```bash
git add index.yaml assets charts
git commit -m "Hiding suse/kubewarden-controller"
git push origin main-source

# Open a Pull Request
```


## Chart Submission Process

1. Fork the [Rancher Partner Charts](https://github.com/rancher/partner-charts/) repository
2. Clone your fork
3. Ensure the 'main-source' branch is checked out
4. Create subdirectories in **packages** in the form of *vendor/chart*
5. Create your **upstream.yaml**
6. Create any add-on files like your app-readme.md and questions.yaml in an 'overlay' subdirectory (Optional)
6. Commit your packages directory
7. Push your commit and open a pull request

```bash
git clone -b main-source git@github.com:rancher/partner-charts.git
cd partner-charts
mkdir -p packages/suse/kubewarden-controller
cat <<EOF > packages/suse/kubewarden-controller/upstream.yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
EOF

mkdir packages/suse/kubewarden-controller/overlay
echo "Example app-readme.md" > packages/suse/kubewarden-controller/overlay/app-readme.md

git add packages/suse/kubewarden-controller
git commit -m "Submitting suse/kubewarden-controller"
git push origin main-source

# Open Your Pull Request
```


### Using the tool

If you would like to test your configuration using this tool, simply run the
provided script to download the tool. The 'auto' function is what will be run to
generate new versions.

The example below assumes we have already committed an **upstream.yaml** to
**packages/suse/kubewarden-controller/upstream.yaml**
```bash
git clone -b main-source git@github.com:rancher/partner-charts.git
cd partner-charts
scripts/pull-ci-scripts
export PACKAGE=suse/kubewarden-controller
bin/partner-charts-ci auto
```


## Command Reference

Some commands respect the `PACKAGE` environment variable. This can be used
to specify a chart in the format as output by the `list` command, `<vendor>/
<chart>`. This environment variable may also be set to just the top level
`<vendor>` directory to apply to all charts contained within that vendor.

| Command | Description |
| ------------- | ------------- |
| list | Lists all charts found with an **upstream.yaml** file in the `packages` directory. If `PACKAGE` environment variable is set, will only list chart(s) that match
| prepare | Included for backwards-compatability. Prepares a copy of the chart in the chart's `packages` directory for modification via GNU patch
| patch | Included for backwards-compatability. Generates patch files after alterations made following `prepare` command
| clean | Included for backwards-compatability. Cleans chart created from `prepare` command
| auto | Automated CI process. Checks all configured charts for updates in upstream, downloads updates, makes necessary alterations, stores chart assets, updates index, and commits changes. If `PACKAGE` environment variable is set, will only check and update specified chart(s)
| stage | Does everything auto does except create the final commit. Useful for testing. If `PACKAGE` environment variable is set, will only check and updated specified chart(s)
| unstage | Equivalent to running `git clean -d -f && git checkout -f .`
| hide | Alters existing chart to add `catalog.cattle.io/hidden: "true"` annotation in index and assets. Accepts one chart name as argument, in the format as printed by `list`
| [feature](#feature) | Alters existing chart to add, remove, or list charts with `catalog.cattle.io/featured` annotation
| validate | Validates current repository against configured released repo in `configuration.yaml` to ensure released assets are not being modified


### Subcommands

#### `feature`
| Command | Arguments | Description |
| ------------- | ------------- | ------------- |
| list | N/A | Lists the current charts with the featured annotation and their associated index. Listed name is the chart name as listed in the `index.yaml`, not the chart name in the `<vendor>/<chart>` format
| add | Accepts two arguemnts. The chart name in the format as printed by the standard `list` command, `<vendor>/<chart>`, and the index to be featured at (1-5) | Adds the `catalog.cattle.io/featured: <index>` annotaton to a given chart
| remove | Accepts one chart name as argument, in the format as printed by the standard `list` command, `<vendor>/<chart>` | Removes the `catalog.cattle.io/featured` annotation from a given chart


### Overlay

Any files placed in the *packages/vendor/chart/overlay* directory will be
overlayed onto the chart. This allows for adding or overwriting files within the
chart as needed. The primary intended purpose is for adding the app-readme.md
and questions.yaml files.


### Configuration File

The tool reads a configuration yaml, `upstream.yaml`, to know where to fetch
the upstream chart. This file is also able to define any alterations for valid
variables in the Chart.yaml as described by [Helm](https://helm.sh/docs/topics/
charts/#the-chart-file-structure).

Options for `upstream.yaml`
| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| AutoInstall | | Allows setting a required additional chart to deploy prior to current chart, such as a dedicated CRDs chart
| ChartMetadata | | Allows setting/overriding the value of any valid Chart.yaml variable
| DisplayName | | Sets the name the chart will be listed under in the Rancher UI
| Experimental | | Adds the 'experimental' annotation which adds a flag on the UI entry
| Fetch | HelmChart, HelmRepo | Selects set of charts to pull from upstream.<br />- **latest** will pull only the latest chart version *default*<br />- **newer** will pull all newer versions than currently stored<br />- **all** will pull all versions
| GitBranch | GitRepo | Defines which branch to pull from the upstream GitRepo
| GitHubRelease | GitRepo | If true, will pull latest GitHub release from repo. Requires GitHub URL
| GitRepo | | Defines the git repo to pull from
| GitSubdirectory | GitRepo | Allows selection of a subdirectory of the upstream git repo to pull the chart from
| HelmChart | HelmRepo | Defines which chart to pull from the upstream Helm repo
| HelmRepo | HelmChart | Defines the upstream Helm repo to pull from
| Hidden | | Adds the 'hidden' annotation which hides the chart from the Rancher UI
| Namespace | | Addes the 'namespace' annotation which hard-codes a deployment namespace for the chart
| PackageVersion | | Used to generate new patch version of chart
| ReleaseName | | Sets the value of the release-name Rancher annotation. Defaults to the chart name
| Vendor | | Sets the vendor name providing the chart


### Helm Repo

```yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
Fetch: newer
ChartMetadata:
  kubeVersion:  '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Artifact Hub

```yaml
---
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Git Repo

```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitBranch: main
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### GitHub Release

```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitHubRelease: true
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```
