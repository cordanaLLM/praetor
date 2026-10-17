package classify

import "testing"

// clusterRepo lays out the repository of #1074: Terraform, Ansible roles, a Helm chart below the
// root, ArgoCD manifests and one Packer template for the node image.
func clusterRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"terraform/main.tf", "ansible/ansible.cfg", "ansible/roles/base/tasks/main.yml",
		"charts/platform/Chart.yaml", "argocd/app.yaml", "packer/node.pkr.hcl"} {
		writeAt(t, root, rel)
	}
	return root
}

// Positive: a cluster repository is classified by what it deploys, gitops-infra, although it
// holds a Packer template; two nested kinds alone (a chart and a kustomization) suffice too.
func TestByMarkers_Positive_ClusterRepositoryIsGitOps(t *testing.T) {
	if got := ByMarkers(clusterRepo(t)); got.Archetype != "gitops-infra" || got.Source != SourceMarkers {
		t.Fatalf("cluster repository gave %+v, want gitops-infra", got)
	}
	root := t.TempDir()
	writeAt(t, root, "deploy/charts/api/Chart.yaml")
	writeAt(t, root, "deploy/overlays/prod/kustomization.yaml")
	if got := ByMarkers(root); got.Archetype != "gitops-infra" {
		t.Fatalf("chart and kustomization below the root gave %+v", got)
	}
	if !HasMarkerOf(clusterRepo(t), "gitops-infra") {
		t.Fatal("HasMarkerOf disagrees with ByMarkers on gitops-infra")
	}
}

// Negative: one kind of infrastructure is a helper, not the product. An image forge with a
// Terraform test environment stays os-image, and an application with its Helm chart below the
// root stays what its build says.
func TestByMarkers_Negative_OneInfrastructureKindDoesNotDecide(t *testing.T) {
	forge := t.TempDir()
	writeAt(t, forge, "packer/ubuntu.pkr.hcl")
	writeAt(t, forge, "test/terraform/main.tf")
	if got := ByMarkers(forge); got.Archetype != "os-image" {
		t.Fatalf("image forge with a Terraform test environment gave %+v", got)
	}
	app := t.TempDir()
	writeAt(t, app, "go.mod")
	writeAt(t, app, "deploy/helm/api/Chart.yaml")
	if got := ByMarkers(app); got.Archetype != "framework" {
		t.Fatalf("application with a nested chart gave %+v", got)
	}
}

// Boundary: a marker three levels below the root counts and one four levels below does not; a
// root Chart.yaml alone keeps the container-image rule.
func TestByMarkers_Boundary_InfrastructureMarkerDepth(t *testing.T) {
	deep := t.TempDir()
	writeAt(t, deep, "main.tf")
	writeAt(t, deep, "a/b/c/Chart.yaml")
	if got := ByMarkers(deep); got.Archetype != "gitops-infra" {
		t.Fatalf("chart three levels down gave %+v", got)
	}
	tooDeep := t.TempDir()
	writeAt(t, tooDeep, "main.tf")
	writeAt(t, tooDeep, "a/b/c/d/Chart.yaml")
	if got := ByMarkers(tooDeep); got.Archetype == "gitops-infra" {
		t.Fatalf("chart four levels down counted: %+v", got)
	}
	chart := t.TempDir()
	writeAt(t, chart, "Chart.yaml")
	if got := ByMarkers(chart); got.Archetype != "container-image" {
		t.Fatalf("root chart alone gave %+v", got)
	}
}
