# partner-charts-ci

`partner-charts-ci` is used to automate the process of working with the
[Rancher Partner Charts Repository](https://github.com/rancher/partner-charts).
More specifically, it uses configuration that specifies "upstream" helm charts to
download those charts, modify them for Rancher, and construct/maintain a new
helm chart repository. It also includes commands for things like validating the
state of the repository and removing out-of-date versions of helm charts.


## The Basics

### Terminology

A **package** is a set of configuration and files that specifies an upstream
helm chart and modifications applied to it when integrating new versions of it
into the repository. There is a single package for every chart in the repository.

A **chart** is a helm chart.

A **chart version** is a specific version of a chart. Each chart included in
the repository has one or more chart versions.

**Upstream** refers to the place a chart comes from. It can take the form of a
helm repository, an Artifact Hub repository or a git repository.


### Directory Structure

```
configuration.yaml                      configuration for partner-charts-ci
index.yaml                              the index.yaml for the helm repository
packages/                               contains package directories
  suse/
    kubewarden-controller/              referred to as a "package directory"
      upstream.yaml                     configuration specific to a single package
      overlay/
        app-readme.md
        questions.yaml
    ...
  ...
charts/                                 unarchived chart versions
  suse/
    kubewarden-controller/
      1.2.3/
        ...
      1.2.4/
        ...
      ...
    ...
  ...
assets/                                 archived chart versions
  suse/
    kubewarden-controller-1.2.3.tgz
    kubewarden-controller-1.2.4.tgz
    ...
  ...
```


### Package Directories

Each package has a package directory. Package directories are of the form
`packages/<vendor>/<chart>/`, where `<vendor>` is the vendor that provides the chart
and `<chart>` is the name of the chart. Taken together, `<vendor>/<chart>` is the full
name used to refer to the package.

Package directories must contain an [`upstream.yaml`](#upstreamyaml) file. Typically
they also contain an `overlay/` directory that contains files that are copied into
the chart directory when integrating new versions of the chart. `overlay/` is most
often used for adding [`app-readme.md`](#app-readmemd) and
[`questions.yaml`](#questionsyaml) files.


## Example Workflow for Adding a Package

These steps assume that the working directory is set to the root of
the repository you want to operate on. They use the example
`suse/kubewarden-controller` package in commands.

#### 1. Create a directory for your package of the form `packages/<vendor>/<chart>`

```bash
mkdir -p packages/suse/kubewarden-controller
```

#### 2. Create an [`upstream.yaml`](#upstreamyaml) file in the package directory

#### 3. Populate `overlay/`

If you want any files to be added when chart versions are integrated into the repo,
now is the time add them to `overlay/`.

```bash
mkdir packages/suse/kubewarden-controller/overlay
echo "Example app-readme.md" > packages/suse/kubewarden-controller/overlay/app-readme.md
```

#### 4. Run `partner-charts-ci update`

`partner-charts-ci update` will download any new chart versions from upstream
and integrate them into the repository. Append the `--commit` flag if you
want `partner-charts-ci` to create a git commit containing these changes.
The `PACKAGE` environment variable allows you to specify a package to operate
on.

```bash
PACKAGE=suse/kubewarden-controller partner-charts-ci update --commit
```

#### 5. Validate your changes

```bash
partner-charts-ci validate
```

#### 6. Test your chart by installing it in the Rancher UI

1. Ensure that you have a fork of the
[Rancher Partner Charts repository](https://github.com/rancher/partner-charts)
in your personal Github account.
2. Push your changes to your fork.
3. In the Rancher UI, create a test repository that points to the branch you pushed
your changes to in your fork. To do this, navigate to `Apps > Repositories` and
click `Create`. Enter a name, the URL of your fork, and the branch containing your
changes, and click `Create` again.
4. Once the new repository is active, you should be able to find your chart
in `Apps > Charts`. Check that the readme is correct, and then install your
chart. It should install successfully.


## File Reference

### `upstream.yaml`

`upstream.yaml` contains package configuration.

> [!IMPORTANT]
> In GKE clusters, a Helm Chart will NOT display in Rancher Apps unless
> `kubeVersion` includes `-0` suffix in `Chart.yaml`. You can set it
> through the `ChartMetadata.kubeVersion` field in the `upstream.yaml`
> file.

| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| AutoInstall | | Allows setting a required additional chart to deploy prior to current chart, such as a dedicated CRDs chart
| ChartMetadata | | Allows setting/overriding the value of any valid [Chart.yaml variable](https://helm.sh/docs/topics/charts/#the-chartyaml-file)
| Deprecated | | Whether the package is deprecated. Deprecated packages will not integrate any new chart versions from upstream. Do not set this field directly; instead, use `partner-charts-ci deprecate`.
| DisplayName | | The name of the chart used in the Rancher UI
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
| PackageVersion | | **Deprecated**. Allows for creating multiple local chart versions from a single upstream chart version. Should not be added to any existing packages, nor should it be defined on any new packages.
| ReleaseName | | Sets the value of the release-name Rancher annotation. Defaults to the chart name
| Vendor | | The name of the vendor used in the Rancher UI

#### Example: Helm Repo

```yaml
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion:  '>=1.21-0'
```

#### Example: Artifact Hub

```yaml
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
```

#### Example: Git Repo

```yaml
GitRepo: https://github.com/kubewarden/helm-charts.git
GitBranch: main
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
```

#### Example: Github Release

```yaml
GitRepo: https://github.com/kubewarden/helm-charts.git
GitHubRelease: true
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
```


### `app-readme.md`

`app-readme.md` is a brief description of the app and how to use it. It
is best to keep it short since the longer `README.md` in your chart will be
displayed in the UI as detailed description.


### `questions.yaml`

`questions.yml` defines a set of questions to display on the chart's
installation page. It allows users to answer them and configure the chart
using the UI instead of modifying the chart's values file directly. You
can find a reference for `questions.yaml`
[here](https://docs.ranchermanager.rancher.io/how-to-guides/new-user-guides/helm-charts-in-rancher/create-apps#question-variable-reference).

#### Example

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


## Other Information

### Hidden Charts

Hidden charts have packages with `Hidden: true` in their `upstream.yaml`. When
`Hidden: true` is present in a package's `upstream.yaml`, `partner-charts-ci`
ensures that all of its chart versions have the `catalog.cattle.io/hidden`
annotation set. This annotation causes the Rancher UI to not show the chart.
If you want to hide a chart, use the `partner-charts-ci hide` subcommand.
Simply setting `Hidden: true` in `upstream.yaml` will not hide existing chart
versions.


### Featured Charts

In the `Apps > Charts` view, the Rancher UI may have a set of tiles at the
top featuring several charts. These are the "featured" charts. Charts that are
featured have the `catalog.cattle.io/featured` annotation set on their latest
chart version. This annotation is reserved for preferred partners that SUSE
has agreed to highlight in the `Apps > Charts` marquee tiles. You can see and
control featured charts using the `partner-charts-ci feature` subcommands.


### Deprecating and Removing Packages

When a package and its chart versions must be removed, it is typical to
deprecate it for some time in order to give users time to migrate away
from it. Packages are deprecated through the use of the
`partner-charts-ci deprecate` subcommand. Deprecating a package has
several effects:

- any new chart versions from upstream are no longer integrated
- the `upstream.yaml` file for the package has `Deprecated: true` set

Once a package has been deprecated, it can be removed. This is done
through the `partner-charts-ci remove` subcommand. Using this subcommand
prevents maintainers from missing files when removing a package.
It is possible to remove a package without first deprecating it by using
the `--force` option, but please make sure you know what you're doing if
you plan on doing this!
