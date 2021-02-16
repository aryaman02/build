// Copyright The Shipwright Contributors
//
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	build "github.com/shipwright-io/build/pkg/apis/build/v1alpha1"
	"github.com/shipwright-io/build/pkg/ctxlog"
	buildmetrics "github.com/shipwright-io/build/pkg/metrics"
	"github.com/shipwright-io/build/pkg/validate"
)

// Reconcile reads that state of the cluster for a Build object and makes changes based on the state read
// and what is in the Build.Spec
func (r *ReconcileBuild) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Set the ctx to be Background, as the top-level context for incoming requests.
	ctx, cancel := context.WithTimeout(r.ctx, r.config.CtxTimeOut)
	defer cancel()

	ctxlog.Debug(ctx, "start reconciling Build", namespace, request.Namespace, name, request.Name)

	b := &build.Build{}
	err := r.client.Get(ctx, request.NamespacedName, b)
	if err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	} else if apierrors.IsNotFound(err) {
		ctxlog.Debug(ctx, "finish reconciling build. build was not found", namespace, request.Namespace, name, request.Name)
		return reconcile.Result{}, nil
	}

	// Populate the status struct with default values
	b.Status.Registered = corev1.ConditionFalse
	b.Status.Reason = build.SucceedStatus

	// build a list of current validation types
	validationTypes := []string{
		validate.OwnerReferences,
		validate.SourceURL,
		validate.Secrets,
		validate.Strategies,
		validate.Runtime,
	}

	// trigger all current validations
	for _, validationType := range validationTypes {
		v, err := validate.NewValidation(validationType, b, r.client, r.scheme)
		if err != nil {
			// when the validation type is unknown
			return reconcile.Result{}, err
		}

		if err := v.ValidatePath(ctx); err != nil {
			// We enqueue another reconcile here. This is done only for validation
			// types where the error can be produced from a failed API call.
			if validationType == validate.Secrets || validationType == validate.Strategies {
				return reconcile.Result{}, err
			}
			if validationType == validate.OwnerReferences {
				// we do not want to bail out here if the owerreference validation fails, we ignore this error on purpose
				// In case we just created the Build, we want the Build reconcile logic to continue, in order to
				// validate the Build references ( e.g secrets, strategies )
				ctxlog.Info(ctx, "unexpected error during ownership reference validation", namespace, request.Namespace, name, request.Name, "error", err)
			}
		}
		if b.Status.Reason != build.SucceedStatus {
			return r.UpdateBuildStatusAndRetreat(ctx, b)
		}
	}

	b.Status.Registered = corev1.ConditionTrue
	b.Status.Message = build.AllValidationsSucceeded
	err = r.client.Status().Update(ctx, b)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Increase Build count in metrics
	buildmetrics.BuildCountInc(b.Spec.StrategyRef.Name, b.Namespace, b.Name)

	ctxlog.Debug(ctx, "finishing reconciling Build", namespace, request.Namespace, name, request.Name)
	return reconcile.Result{}, nil
}

// UpdateBuildStatusAndRetreat returns an error if an update fails, this should force
// a new reconcile until the API call succeeds. If return is nil, no further reconciliations
// will take place
func (r *ReconcileBuild) UpdateBuildStatusAndRetreat(ctx context.Context, b *build.Build) (reconcile.Result, error) {
	if err := r.client.Status().Update(ctx, b); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
