/*
Copyright 2019 The OpenEBS Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"github.com/openebs/maya/pkg/alertlog"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog"

	pvController "sigs.k8s.io/sig-storage-lib-external-provisioner/controller"
	//pvController "github.com/kubernetes-sigs/sig-storage-lib-external-provisioner/controller"
	"github.com/openebs/dynamic-localpv-provisioner/pkg/kubernetes/api/core/v1/persistentvolume"
	mconfig "github.com/openebs/maya/pkg/apis/openebs.io/v1alpha1"
)

const (
	Type     = "type"
	XfsQuota = "xfs-quota"
	Bhard    = "bhard"
	Bsoft    = "bsoft"
)

// ProvisionHostPath is invoked by the Provisioner which expect HostPath PV
//  to be provisioned and a valid PV spec returned.
func (p *Provisioner) ProvisionHostPath(opts pvController.ProvisionOptions, volumeConfig *VolumeConfig) (*v1.PersistentVolume, error) {
	pvc := opts.PVC
	taints := GetTaints(opts.SelectedNode)
	name := opts.PVName
	stgType := volumeConfig.GetStorageType()
	saName := getOpenEBSServiceAccountName()
	//capacity := opts.PVC.Spec.Resources.Requests[v1.ResourceStorage]
	scType := opts.StorageClass.Parameters[Type]
	bsoft := opts.StorageClass.Parameters[Bsoft]
	bhard := opts.StorageClass.Parameters[Bhard]

	nodeAffinityKey := volumeConfig.GetNodeAffinityLabelKey()
	if len(nodeAffinityKey) == 0 {
		nodeAffinityKey = k8sNodeLabelKeyHostname
	}
	nodeAffinityValue := GetNodeLabelValue(opts.SelectedNode, nodeAffinityKey)

	path, err := volumeConfig.GetPath()
	if err != nil {
		alertlog.Logger.Errorw("",
			"eventcode", "local.pv.provision.failure",
			"msg", "Failed to provision Local PV",
			"rname", opts.PVName,
			"reason", "Unable to get volume config",
			"storagetype", stgType,
		)
		return nil, err
	}

	imagePullSecrets := GetImagePullSecrets(getOpenEBSImagePullSecrets())

	klog.Infof("Creating volume %v at node with label %v=%v, path:%v,ImagePullSecrets:%v", name, nodeAffinityKey, nodeAffinityValue, path, imagePullSecrets)

	//Before using the path for local PV, make sure it is created.
	initCmdsForPath := []string{"mkdir", "-m", "0777", "-p"}
	podOpts := &HelperPodOptions{
		cmdsForPath:            initCmdsForPath,
		name:                   name,
		path:                   path,
		nodeAffinityLabelKey:   nodeAffinityKey,
		nodeAffinityLabelValue: nodeAffinityValue,
		//capacity:               capacity.String(),
		scType:             scType,
		bsoft:              bsoft,
		bhard:              bhard,
		serviceAccountName: saName,
		selectedNodeTaints: taints,
		imagePullSecrets:   imagePullSecrets,
	}
	iErr := p.createInitPod(podOpts)
	if iErr != nil {
		klog.Infof("Initialize volume %v failed: %v", name, iErr)
		alertlog.Logger.Errorw("",
			"eventcode", "local.pv.provision.failure",
			"msg", "Failed to provision Local PV",
			"rname", opts.PVName,
			"reason", "Volume initialization failed",
			"storagetype", stgType,
		)
		return nil, iErr
	}

	//
	if scType == XfsQuota && (len(bsoft) != 0 || len(bhard) != 0) {
		// Check when both limits are specified, they are valid limits
		// Validation: bsoft shouldn't be greater than bhard
		if len(bsoft) != 0 && len(bhard) != 0 {
			err := ValidateLimits(bsoft, bhard)
			if err != nil {
				klog.Infof("Setting xfs quota for volume %v failed: %v", name, err)
				alertlog.Logger.Errorw("",
					"eventcode", "local.pv.quota.failure",
					"msg", "Failed to set xfs quota limits for Local PV",
					"rname", opts.PVName,
					"reason", "xfs quota validation failed",
					"storagetype", stgType,
				)
				return nil, err
			}
		}

		xfsQuotaErr := p.createXfsQuotaComponents(podOpts)
		if xfsQuotaErr != nil {
			klog.Infof("Setting xfs quota for volume %v failed: %v", name, xfsQuotaErr)
			alertlog.Logger.Errorw("",
				"eventcode", "local.pv.quota.failure",
				"msg", "Failed to set xfs quota limits for Local PV",
				"rname", opts.PVName,
				"reason", "Setting xfs quota failed",
				"storagetype", stgType,
			)
			return nil, xfsQuotaErr
		}

		alertlog.Logger.Infow("",
			"eventcode", "local.pv.quota.success",
			"msg", "Successfully applied xfs quota limits to the Local PV volume",
			"rname", opts.PVName,
			"storagetype", stgType,
		)
	}

	// VolumeMode will always be specified as Filesystem for host path volume,
	// and the value passed in from the PVC spec will be ignored.
	fs := v1.PersistentVolumeFilesystem

	// It is possible that the HostPath doesn't already exist on the node.
	// Set the Local PV to create it.
	//hostPathType := v1.HostPathDirectoryOrCreate

	// TODO initialize the Labels and annotations
	// Use annotations to specify the context using which the PV was created.
	//volAnnotations := make(map[string]string)
	//volAnnotations[string(v1alpha1.CASTypeKey)] = casVolume.Spec.CasType
	//fstype := casVolume.Spec.FSType

	labels := make(map[string]string)
	labels[string(mconfig.CASTypeKey)] = "local-" + stgType
	//labels[string(v1alpha1.StorageClassKey)] = *className

	//TODO Change the following to a builder pattern
	pvObj, err := persistentvolume.NewBuilder().
		WithName(name).
		WithLabels(labels).
		WithReclaimPolicy(*opts.StorageClass.ReclaimPolicy).
		WithAccessModes(pvc.Spec.AccessModes).
		WithVolumeMode(fs).
		WithCapacityQty(pvc.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]).
		WithLocalHostDirectory(path).
		WithNodeAffinity(nodeAffinityKey, nodeAffinityValue).
		Build()

	if err != nil {
		alertlog.Logger.Errorw("",
			"eventcode", "local.pv.provision.failure",
			"msg", "Failed to provision Local PV",
			"rname", opts.PVName,
			"reason", "failed to build persistent volume",
			"storagetype", stgType,
		)
		return nil, err
	}
	alertlog.Logger.Infow("",
		"eventcode", "local.pv.provision.success",
		"msg", "Successfully provisioned Local PV",
		"rname", opts.PVName,
		"storagetype", stgType,
	)
	return pvObj, nil
}

//
func ValidateLimits(bsoft, bhard string) error {
	//var bsoftLimit, bhardLimit resource.Quantity
	bsoftLimit, err := resource.ParseQuantity(bsoft)
	if err != nil {
		return err
	}
	bhardLimit, err := resource.ParseQuantity(bhard)
	if err != nil {
		return err
	}
	if bsoftLimit.Cmp(bhardLimit) > 0 {
		error := errors.New("bsoft limit is greater than bhard limit")
		return error
	}
	return nil
}

// GetNodeObjectFromLabels returns the Node Object with matching label key and value
func (p *Provisioner) GetNodeObjectFromLabels(key, value string) (*v1.Node, error) {
	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{key: value}}
	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
	}
	nodeList, err := p.kubeClient.CoreV1().Nodes().List(listOptions)
	if err != nil || len(nodeList.Items) == 0 {
		// After the PV is created and node affinity is set
		// based on kubernetes.io/hostname label, either:
		// - hostname label changed on the node or
		// - the node is deleted from the cluster.
		return nil, errors.Errorf("Unable to get the Node with the Node Label %s [%s]", key, value)
	}
	if len(nodeList.Items) != 1 {
		// After the PV is created and node affinity is set
		// on a custom affinity label, there may be a transitory state
		// with two nodes matching (old and new) label.
		return nil, errors.Errorf("Unable to determine the Node. Found multiple nodes matching the labels %s [%s].", key, value)
	}
	return &nodeList.Items[0], nil

}

// DeleteHostPath is invoked by the PVC controller to perform clean-up
//  activities before deleteing the PV object. If reclaim policy is
//  set to not-retain, then this function will create a helper pod
//  to delete the host path from the node.
func (p *Provisioner) DeleteHostPath(pv *v1.PersistentVolume) (err error) {
	defer func() {
		err = errors.Wrapf(err, "failed to delete volume %v", pv.Name)
	}()

	saName := getOpenEBSServiceAccountName()
	//Determine the path and node of the Local PV.
	pvObj := persistentvolume.NewForAPIObject(pv)
	path := pvObj.GetPath()
	if path == "" {
		return errors.Errorf("no HostPath set")
	}

	nodeAffinityKey, nodeAffinityValue := pvObj.GetAffinitedNodeLabelKeyAndValue()
	if nodeAffinityValue == "" {
		return errors.Errorf("cannot find affinited node details")
	}
	alertlog.Logger.Infof("Get the Node Object with label %v : %v", nodeAffinityKey, nodeAffinityValue)

	//Get the node Object once again to get updated Taints.
	nodeObject, err := p.GetNodeObjectFromLabels(nodeAffinityKey, nodeAffinityValue)
	if err != nil {
		return err
	}
	taints := GetTaints(nodeObject)

	imagePullSecrets := GetImagePullSecrets(getOpenEBSImagePullSecrets())

	//Initiate clean up only when reclaim policy is not retain.
	klog.Infof("Deleting volume %v at %v:%v", pv.Name, GetNodeHostname(nodeObject), path)
	cleanupCmdsForPath := []string{"rm", "-rf"}
	podOpts := &HelperPodOptions{
		cmdsForPath:            cleanupCmdsForPath,
		name:                   pv.Name,
		path:                   path,
		nodeAffinityLabelKey:   nodeAffinityKey,
		nodeAffinityLabelValue: nodeAffinityValue,
		serviceAccountName:     saName,
		selectedNodeTaints:     taints,
		imagePullSecrets:       imagePullSecrets,
	}

	if err := p.createCleanupPod(podOpts); err != nil {
		return errors.Wrapf(err, "clean up volume %v failed", pv.Name)
	}
	return nil
}
