// Copyright 2026 Intrinsic Innovation LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package runtimedbtransfer contains utilities for populating runtime dbs.
package runtimedbtransfer

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	runtime "intrinsic/assets/conversion/runtime"
	"intrinsic/assets/deploy/privileges"
	"intrinsic/assets/idutils"
	"intrinsic/assets/install/gather"
	localconv "intrinsic/assets/install/localconv"
	"intrinsic/resources/service/resourcetyperuntime"
	"intrinsic/skills/internal/skillruntime"
	"intrinsic/util/go/xiter"
	"intrinsic/util/grpc/statusutil"

	backoff "github.com/cenkalti/backoff/v4"
	log "github.com/golang/glog"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	acigrpcpb "intrinsic/assets/catalog/proto/v1/asset_catalog_internal_go_proto"
	atpb "intrinsic/assets/proto/asset_type_go_proto"
	idpb "intrinsic/assets/proto/id_go_proto"
	applicationpb "intrinsic/config/proto/application_go_proto"
	rcigrpcpb "intrinsic/resources/catalog/proto/resource_catalog_internal_go_proto"
	rtrpb "intrinsic/resources/proto/resource_type_runtime_go_proto"
	srpb "intrinsic/skills/proto/skill_runtime_go_proto"
)

const (
	// maxRetryAttempts is the maximum number of times to retry populating
	// resource runtime data.
	maxRetryAttempts = 5
)

type catalogPull struct {
	idv                      *idpb.IdVersion
	fromSkillIDVersionsField bool
	validate                 bool
}

func isSkill(rt *rtrpb.ResourceTypeRuntime) bool {
	return rt.GetMetadata().GetAssetType() == atpb.AssetType_ASSET_TYPE_SKILL
}

func toIDVersion(p catalogPull) *idpb.IdVersion {
	return p.idv
}

// gatherAssets collects assets in an application from the assets and skills
// fields. It returns a mapping between asset IDVersion and its runtime data, a
// mapping between asset ID and an IDVersion to be pulled from the asset
// catalog, and a mapping between asset ID and its corresponding string
// IDVersion. The last map is only used to check for version mismatches.
func gatherAssets(ctx context.Context, app *applicationpb.Application, local []*rtrpb.ResourceTypeRuntime, opts GatherResourceTypesOpts) (map[string]*rtrpb.ResourceTypeRuntime, map[string]catalogPull, error) {
	// Construct and collect all resource type runtimes from assets that were
	// declared locally or collect their asset idversions so that they can be
	// pulled from the catalog.
	rts := make(map[string]*rtrpb.ResourceTypeRuntime)
	pullFromCatalog := make(map[string]catalogPull)
	assetIDToIDVersion := make(map[string]string)
	for _, a := range app.GetAssets() {
		var rtr *rtrpb.ResourceTypeRuntime
		var err error
		switch v := a.GetVariant().(type) {
		case *applicationpb.Application_Asset_Catalog:
			idv := v.Catalog
			if err := idutils.ValidateIDVersionProto(idv); err != nil {
				return nil, nil, status.Errorf(codes.InvalidArgument, "catalog asset %q has an invalid id version: %v", idv, err)
			}
			id := idutils.IDFromProtoUnchecked(idv.GetId())
			idvString := idutils.IDVersionFromProtoUnchecked(idv)
			if pidv, exists := assetIDToIDVersion[id]; exists && idv.GetVersion() != pidv {
				return nil, nil, status.Errorf(codes.InvalidArgument, "application contains multiple assets with the same ID %q but different versions", id)
			}
			pullFromCatalog[idvString] = catalogPull{
				idv:      idv,
				validate: true,
			}
			assetIDToIDVersion[id] = idvString
			continue
		default:
			rtr, err = localconv.LocalSolutionAssetToRuntime(ctx, a, runtime.WithACIClient(opts.ACIClient))
			if err != nil {
				return nil, nil, statusutil.Wrap(err, "could not gather asset")
			}

		}
		id := idutils.IDFromProtoUnchecked(rtr.GetMetadata().GetIdVersion().GetId())
		idv := idutils.IDVersionFromProtoUnchecked(rtr.GetMetadata().GetIdVersion())
		if pidv, exists := assetIDToIDVersion[id]; exists && idv != pidv {
			return nil, nil, status.Errorf(codes.InvalidArgument, "application contains multiple assets with the same ID %q but different versions", id)
		}
		if err := opts.Validator.Validate(rtr); err != nil {
			return nil, nil, status.Errorf(codes.PermissionDenied, "unable to install %q: %v", idv, err)
		}
		rts[idv] = rtr
		assetIDToIDVersion[id] = idv
	}

	// Collect runtime data from the modified solution. If an asset is already
	// marked to be pulled from the catalog we will remove it from the list.
	for _, rtr := range local {
		idv := idutils.IDVersionFromProtoUnchecked(rtr.GetMetadata().GetIdVersion())
		delete(pullFromCatalog, idv)
		rts[idv] = rtr
	}

	// Prior to the installed assets service, nothing maintained the invariant
	// that there was a single version of a skill in the application / modified
	// solution.  A skill could be sideloaded, which would overwrite the skill
	// chart and be stored in the modified solution, but the entry in the
	// catalog skills in the application proto would remain.  What we do in the
	// next lines is preserve the behavior effectively enforced by the transfer
	// service and the chart assignment controller if it saw the same chart
	// multiple times, which would be to take the last one listed, since it
	// would be applied last, and thus overwrite anything prior.  The existing
	// behavior was that skills would be retrieved in the order listed in the
	// catalog skills, and then the skills from the modified solution proto
	// would be added.  The result is that sideloaded skills should always be
	// prioritized, then the last skill of any id in the catalog skills.  The
	// modified solution service when saving skills always looks at the skill
	// charts, which means that we know it maintains the invariant that there
	// is one skill per id.  The logic below keeps the behavior while only
	// adding a single entry per id for skills.
	// Walk through the catalog skills and save the last skill per id.
	catalogSkillByID := make(map[string]*idpb.IdVersion)
	for _, idv := range app.GetSkills().GetSkillIdVersions() {
		parts, err := idutils.NewIDVersionParts(idv)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "catalog skill %q has an invalid id version: %v", idv, err)
		}
		catalogSkillByID[parts.ID()] = parts.IDVersionProto()
	}
	// Remove any skills that were already sideloaded from the list to retrieve
	// from the catalog.
	for rt := range xiter.Filter(isSkill, maps.Values(rts)) {
		delete(catalogSkillByID, idutils.IDFromProtoUnchecked(rt.GetMetadata().GetIdVersion().GetId()))
	}
	for _, idv := range catalogSkillByID {
		pullFromCatalog[idutils.IDVersionFromProtoUnchecked(idv)] = catalogPull{
			idv:                      idv,
			fromSkillIDVersionsField: true,
			validate:                 false,
		}
	}

	// We pull out asset runtime data from the resource set. Resource instances
	// that reference an asset ID that is already present in the modified solution
	// are skipped over. Otherwise we do check and error out if an instance
	// includes an asset ID that matches an asset declared elsewhere but whose
	// version doesn't match.
	for _, ri := range app.GetResources().GetResourceInstances() {
		idv := ri.GetTypeIdVersion()
		if idv == "" {
			return nil, nil, status.Errorf(codes.InvalidArgument, "resource instance %q missing type information", ri.GetName())
		}
		parts, err := idutils.NewIDVersionParts(idv)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "%q has an invalid id version: %v", ri.GetName(), err)
		}

		// Skip any resource types that have already been accounted for.
		if _, exists := rts[idv]; !exists {
			pullFromCatalog[idv] = catalogPull{
				idv:      parts.IDVersionProto(),
				validate: false,
			}
		}
	}

	return rts, pullFromCatalog, nil
}

func pullSkillsFromSkillIDVersionsField(ctx context.Context, pullFromCatalog map[string]catalogPull, rts map[string]*rtrpb.ResourceTypeRuntime, opts GatherResourceTypesOpts) error {
	skills := map[string]catalogPull{}
	for idv, pfc := range pullFromCatalog {
		if !pfc.fromSkillIDVersionsField {
			continue
		}
		skills[idv] = pfc
	}

	if len(skills) == 0 {
		return nil
	}
	gatherer := gather.FromAssetCatalog(
		opts.ACIClient,
		gather.WithAllowedAssetTypes([]atpb.AssetType{atpb.AssetType_ASSET_TYPE_SKILL}),
		gather.WithSkipUnavailable(true),
	)
	rtrs, unavailable, err := gatherer(ctx, slices.Collect(xiter.Map(toIDVersion, maps.Values(skills))))
	if err != nil {
		return statusutil.Wrap(err, "could not gather skill asset")
	}
	for _, rt := range rtrs {
		idv := idutils.IDVersionFromProtoUnchecked(rt.GetMetadata().GetIdVersion())
		if skills[idv].validate {
			if err := opts.Validator.Validate(rt); err != nil {
				return status.Errorf(codes.PermissionDenied, "unable to install %q: %v", idv, err)
			}
		}
		delete(skills, idv)
		delete(pullFromCatalog, idv)
		rts[idv] = rt
	}
	if len(skills) > 0 {
		unavailableReasons := make([]string, 0, len(skills))
		for idv, reason := range unavailable {
			unavailableReasons = append(unavailableReasons, fmt.Sprintf("%s: %s", idv, reason))
		}
		log.WarningContextf(ctx, "Unable to retrieve skills from the AssetCatalog:\n%s", strings.Join(unavailableReasons, "\n"))
	}

	return nil
}

func pullAssetsExceptSkillsFromSkillIDVersionsField(ctx context.Context, pullFromCatalog map[string]catalogPull, rts map[string]*rtrpb.ResourceTypeRuntime, opts GatherResourceTypesOpts) error {
	assets := map[string]catalogPull{}
	for idv, pfc := range pullFromCatalog {
		if pfc.fromSkillIDVersionsField {
			continue
		}
		assets[idv] = pfc
	}

	// We retrieve all non-skill assets from the catalog. Missing assets generate an error.
	gatherer := gather.FromCatalogs(opts.ACIClient, opts.RCIClient)
	rtrs, _, err := gatherer(ctx, slices.Collect(xiter.Map(toIDVersion, maps.Values(assets))))
	if err != nil {
		return statusutil.Wrap(err, "could not gather asset")
	}
	for _, rt := range rtrs {
		idv := idutils.IDVersionFromProtoUnchecked(rt.GetMetadata().GetIdVersion())
		if assets[idv].validate {
			if err := opts.Validator.Validate(rt); err != nil {
				return status.Errorf(codes.PermissionDenied, "unable to install %q: %v", idv, err)
			}
		}
		delete(assets, idv)
		delete(pullFromCatalog, idv)
		rts[idv] = rt
	}
	return nil
}

// GatherResourceTypesOpts provides options for GatherResourceTypes.
type GatherResourceTypesOpts struct {
	ACIClient acigrpcpb.AssetCatalogInternalClient
	RCIClient rcigrpcpb.ResourceCatalogInternalClient
	Validator privileges.Validator
}

// GatherResourceTypes collects all resource types needed by this application and places them in a
// map. For now, this information either comes from the type proto embedded in the resource
// instance, save-data (ModifiedSolution) or it is pulled from the catalogs.
func GatherResourceTypes(ctx context.Context, app *applicationpb.Application, local []*rtrpb.ResourceTypeRuntime, opts GatherResourceTypesOpts) (map[string]*rtrpb.ResourceTypeRuntime, error) {
	ctx, span := trace.StartSpan(ctx, "GatherResourceTypes")
	defer span.End()

	rts, pullFromCatalog, err := gatherAssets(ctx, app, local, opts)
	if err != nil {
		return nil, err
	}

	if err := pullSkillsFromSkillIDVersionsField(ctx, pullFromCatalog, rts, opts); err != nil {
		return nil, err
	}
	if err := pullAssetsExceptSkillsFromSkillIDVersionsField(ctx, pullFromCatalog, rts, opts); err != nil {
		return nil, err
	}

	// We rekey the resource runtimes by asset IDVersion.
	rtsByIDVersion := make(map[string]*rtrpb.ResourceTypeRuntime)
	for _, rt := range rts {
		idv := idutils.IDVersionFromProtoUnchecked(rt.GetMetadata().GetIdVersion())
		rtsByIDVersion[idv] = rt
	}
	return rtsByIDVersion, nil
}

// Retrieve pulls the necessary information from RuntimeDB for a given application.
func Retrieve(ctx context.Context, rtrClient resourcetyperuntime.Client, app *applicationpb.Application) (map[string]*rtrpb.ResourceTypeRuntime, error) {
	rts := make(map[string]*rtrpb.ResourceTypeRuntime)

	var need []string
	seen := make(map[string]struct{})
	for _, ri := range app.GetResources().GetResourceInstances() {
		if _, ok := seen[ri.GetTypeIdVersion()]; !ok {
			need = append(need, ri.GetTypeIdVersion())
			seen[ri.GetTypeIdVersion()] = struct{}{}
		}
	}
	if len(need) == 0 {
		return rts, nil
	}
	rtrs, err := rtrClient.BatchGet(ctx, need)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve needed assets from runtime db: %w", err)
	}
	for _, rtr := range rtrs {
		rts[idutils.IDVersionFromProtoUnchecked(rtr.GetMetadata().GetIdVersion())] = rtr
	}

	return rts, nil
}

// PopulateResourceRuntimeTypes adds every resource type used by this
// application to the runtime DB running on the cluster.
func PopulateResourceRuntimeTypes(ctx context.Context, rts map[string]*rtrpb.ResourceTypeRuntime, rtrClient resourcetyperuntime.Client) error {
	b := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetryAttempts)
	// We use a retry when adding resource information to the runtime DB. We have
	// observed some cases where the runtime DB server is not immediately ready
	// when this function executes.
	// TODO(b/280652643): figure out why this retry is necessary.
	return backoff.Retry(func() error {
		log.InfoContext(ctx, "attempting to clear the runtime database")
		if err := rtrClient.Clear(ctx); err != nil {
			log.ErrorContextf(ctx, "clearing the runtime database failed: %v", err)
			return fmt.Errorf("could not clear asset information from the runtime database: %w", err)
		}
		log.InfoContext(ctx, "cleared the runtime database")
		log.InfoContextf(ctx, "attempting to add %d assets to the runtime database", len(rts))
		if err := rtrClient.BatchPut(ctx, slices.Collect(maps.Values(rts))); err != nil {
			log.ErrorContextf(ctx, "BatchPut to the runtime database failed: %v", err)
			return fmt.Errorf("could not add asset information to runtime db: %w", err)
		}
		log.InfoContextf(ctx, "added %d assets to the runtime database", len(rts))
		return nil
	}, b)
}

// PopulateSkillRuntimesForPBTBasedSkills retrieves all PBT-based skill
// references in the application and adds them to the runtime DB running on the
// cluster.
func PopulateSkillRuntimesForPBTBasedSkills(ctx context.Context, srs []*srpb.SkillRuntime, srClient skillruntime.Client) error {
	b := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetryAttempts)
	// We use a retry when adding resource information to the runtime DB. We have
	// observed some cases where the runtime DB server is not immediately ready
	// when this function executes.
	// TODO(b/280652643): figure out why this retry is necessary.
	return backoff.Retry(func() error {
		log.InfoContext(ctx, "attempting to clear the runtime database")
		if err := srClient.Clear(ctx); err != nil {
			log.ErrorContextf(ctx, "clearing the runtime database failed: %v", err)
			return fmt.Errorf("could not clear asset information from the runtime database: %w", err)
		}
		log.InfoContext(ctx, "cleared the runtime database")

		log.InfoContextf(ctx, "attempting to add %d PBT skills to the runtime db", len(srs))
		if err := srClient.BatchPut(ctx, srs); err != nil {
			log.ErrorContextf(ctx, "BatchPut to the runtime database failed: %v", err)
			return fmt.Errorf("could not add PBT skills to the runtime db: %w", err)
		}
		log.InfoContextf(ctx, "added %d PBT skills to the runtime db", len(srs))
		return nil
	}, b)
}
