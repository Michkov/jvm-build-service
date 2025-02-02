package artifactbuildrequest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"github.com/redhat-appstudio/jvm-build-service/pkg/reconciler/dependencybuild"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/redhat-appstudio/jvm-build-service/pkg/apis/jvmbuildservice/v1alpha1"
)

const (
	//TODO eventually we'll need to decide if we want to make this tuneable
	contextTimeout           = 300 * time.Second
	TaskRunLabel             = "jvmbuildservice.io/artifactbuildrequest-taskrun"
	ArtifactBuildRequestKind = "ArtifactBuildRequest"
)

type ReconcileArtifactBuildRequest struct {
	client client.Client
	scheme *runtime.Scheme
}

func newReconciler(mgr ctrl.Manager) reconcile.Reconciler {
	return &ReconcileArtifactBuildRequest{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
	}
}

func (r *ReconcileArtifactBuildRequest) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	//log := log.FromContext(ctx)
	abr := v1alpha1.ArtifactBuildRequest{}
	err := r.client.Get(ctx, request.NamespacedName, &abr)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	switch abr.Status.State {
	case v1alpha1.ArtifactBuildRequestStateNew, "":
		// create task run
		tr := pipelinev1beta1.TaskRun{}
		tr.Spec.TaskRef = &pipelinev1beta1.TaskRef{Name: "lookup-artifact-location", Kind: pipelinev1beta1.NamespacedTaskKind}
		tr.Namespace = abr.Namespace
		tr.GenerateName = abr.Name + "-scm-discovery-"
		tr.Labels = map[string]string{TaskRunLabel: ""}
		tr.Spec.Params = append(tr.Spec.Params, pipelinev1beta1.Param{Name: "GAV", Value: pipelinev1beta1.ArrayOrString{Type: pipelinev1beta1.ParamTypeString, StringVal: abr.Spec.GAV}})
		if err := controllerutil.SetOwnerReference(&abr, &tr, r.scheme); err != nil {
			return reconcile.Result{}, err
		}
		if err = r.client.Create(ctx, &tr); err != nil {
			return reconcile.Result{}, err
		}
		abr.Status.State = v1alpha1.ArtifactBuildRequestStateDiscovering
		if err = r.client.Status().Update(ctx, &abr); err != nil {
			return reconcile.Result{}, err
		}
	case v1alpha1.ArtifactBuildRequestStateDiscovered:
		//now let's create the dependency build object
		//once this object has been created its resolver takes over
		if abr.Status.Tag == "" {
			//this is a failure
			abr.Status.State = v1alpha1.ArtifactBuildRequestStateMissing
			return reconcile.Result{}, r.client.Status().Update(ctx, &abr)
		}
		//we generate a hash of the url, tag and path for
		//our unique identifier
		hash := md5.Sum([]byte(abr.Status.SCMURL + abr.Status.Tag + abr.Status.Path))
		depId := hex.EncodeToString(hash[:])
		//now lets look for an existing build object
		list := &v1alpha1.DependencyBuildList{}
		lbls := map[string]string{
			dependencybuild.IdLabel: depId,
		}
		listOpts := &client.ListOptions{
			Namespace:     abr.Namespace,
			LabelSelector: labels.SelectorFromSet(lbls),
		}

		if err = r.client.List(ctx, list, listOpts); err != nil {
			return reconcile.Result{}, err
		}
		if len(list.Items) == 0 {
			//no existing build object found, lets create one
			db := &v1alpha1.DependencyBuild{}
			db.Namespace = abr.Namespace
			db.Labels = lbls
			//TODO: name should be based on the git repo, not the abr, but needs
			//a sanitization algorithm
			db.GenerateName = abr.Name + "-"
			db.Spec = v1alpha1.DependencyBuildSpec{
				SCMURL:  abr.Status.SCMURL,
				SCMType: abr.Status.SCMType,
				Tag:     abr.Status.Tag,
				Path:    abr.Status.Path,
			}
			if err := controllerutil.SetOwnerReference(&abr, db, r.scheme); err != nil {
				return reconcile.Result{}, err
			}

			if err = r.client.Create(ctx, db); err != nil {
				return reconcile.Result{}, err
			}
		} else {
			//build already exists, add us to the owner references
			for _, db := range list.Items {
				found := false
				for _, owner := range db.OwnerReferences {
					if owner.UID == abr.UID {
						found = true
						break
					}
				}
				if !found {
					if err := controllerutil.SetOwnerReference(&abr, &db, r.scheme); err != nil {
						return reconcile.Result{}, err
					}
				}
				if err = r.client.Update(ctx, &db); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		//add the dependency build label to the abr as well
		//so you can easily look them up
		if abr.Labels == nil {
			abr.Labels = map[string]string{}
		}
		abr.Labels[dependencybuild.IdLabel] = depId
		if err := r.client.Update(ctx, &abr); err != nil {
			return reconcile.Result{}, err
		}
		//move the state to building
		abr.Status.State = v1alpha1.ArtifactBuildRequestStateBuilding
		return reconcile.Result{}, r.client.Status().Update(ctx, &abr)

	}
	return reconcile.Result{}, nil
}
