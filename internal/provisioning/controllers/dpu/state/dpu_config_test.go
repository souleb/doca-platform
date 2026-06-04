/*
Copyright 2026 NVIDIA

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

package state_test

import (
	"time"

	provisioningv1 "github.com/nvidia/doca-platform/api/provisioning/v1alpha1"
	"github.com/nvidia/doca-platform/internal/provisioning/controllers/dpu/state"
	dutil "github.com/nvidia/doca-platform/internal/provisioning/controllers/dpu/util"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("Phase DPUConfig", func() {
	var defaultDPUName = "dpu-config-test"
	expectDPUConfigCondition := func(status provisioningv1.DPUStatus, wantStatus metav1.ConditionStatus, wantReason, wantMessage string) {
		cond := meta.FindStatusCondition(status.Conditions, provisioningv1.DPUCondDPUConfig.String())
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(wantStatus))
		Expect(cond.Reason).To(Equal(wantReason))
		Expect(cond.Message).To(Equal(wantMessage))
	}

	Context("waiting for agent", func() {
		osInstalledCondition := func(transitionTime metav1.Time) metav1.Condition {
			return metav1.Condition{
				Type:               provisioningv1.DPUCondOSInstalled.String(),
				Status:             metav1.ConditionTrue,
				Reason:             "OsInstalled",
				Message:            "OS installed, waiting for the DPU agent to start",
				LastTransitionTime: transitionTime,
			}
		}

		It("should wait when AgentStatus is nil", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))
		})

		It("should wait when RebootMethod is nil", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: ptr.To(metav1.Now()),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))
		})

		It("should wait when RebootMethod is Unknown even if LastStartupTime changed", func() {
			oldTime := metav1.NewTime(metav1.Now().Add(-time.Hour))
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.AgentLastStartupTime = &oldTime
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: ptr.To(metav1.Now()),
				RebootMethod:    ptr.To(provisioningv1.RebootMethodUnknown),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))
			Expect(status.AgentLastStartupTime).To(Equal(&oldTime), "should not update AgentLastStartupTime while waiting for real RebootMethod")
		})

		It("should wait before the initial agent startup timeout expires", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.PreviousPhase = provisioningv1.DPUOSInstalling
			dpu.Status.Conditions = []metav1.Condition{
				osInstalledCondition(metav1.NewTime(time.Now().Add(-19 * time.Minute))),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))
			expectDPUConfigCondition(status, metav1.ConditionFalse, "WaitingForRebootMethod", "waiting for DPU agent to report reboot method")
		})

		It("should error when the initial agent startup timeout expires", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.PreviousPhase = provisioningv1.DPUOSInstalling
			dpu.Status.Conditions = []metav1.Condition{
				osInstalledCondition(metav1.NewTime(time.Now().Add(-21 * time.Minute))),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUError))

			expectDPUConfigCondition(status, metav1.ConditionFalse, "DPUAgentNotStarted", "DPU agent did not report startup within 20 minutes after OS installation completed")
		})

		It("should not timeout after controller restart when agent startup was already reported", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.PreviousPhase = provisioningv1.DPUOSInstalling
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: ptr.To(metav1.Now()),
			}
			dpu.Status.Conditions = []metav1.Condition{
				osInstalledCondition(metav1.NewTime(time.Now().Add(-21 * time.Minute))),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))

			expectDPUConfigCondition(status, metav1.ConditionFalse, "WaitingForRebootMethod", "waiting for DPU agent to report reboot method")
		})

	})

	Context("stale RebootMethod guard", func() {
		It("should wait when AgentLastStartupTime equals LastStartupTime", func() {
			now := metav1.Now()
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.AgentLastStartupTime = &now
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: &now,
				RebootMethod:    ptr.To(provisioningv1.RebootMethodNoAction),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig), "should not advance when startup time has not changed")
		})
	})

	Context("DPUWarmReboot", func() {
		It("should stay in DPUConfig phase when RebootMethod is DPUWarmReboot", func() {
			oldTime := metav1.NewTime(metav1.Now().Add(-time.Hour))
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.AgentLastStartupTime = &oldTime
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: ptr.To(metav1.Now()),
				RebootMethod:    ptr.To(provisioningv1.RebootMethodDPUWarmReboot),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUConfig))
		})
	})

	Context("transitioning to host reboot", func() {
		It("should clear stale Rebooted condition when entering DPURebooting", func() {
			oldTime := metav1.NewTime(metav1.Now().Add(-time.Hour))
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			dpu.Status.AgentLastStartupTime = &oldTime
			dpu.Status.Conditions = []metav1.Condition{{
				Type:    provisioningv1.DPUCondRebooted.String(),
				Status:  metav1.ConditionTrue,
				Reason:  "Rebooted",
				Message: "stale reboot completion from previous cycle",
			}}
			dpu.Status.AgentStatus = &provisioningv1.AgentStatus{
				LastStartupTime: ptr.To(metav1.Now()),
				RebootMethod:    ptr.To(provisioningv1.RebootMethodSystemLevelReset),
			}

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPURebooting))
			Expect(meta.FindStatusCondition(status.Conditions, provisioningv1.DPUCondRebooted.String())).To(BeNil())
		})
	})

	Context("deletion", func() {
		It("should transition to DPUDeleting when DPU is being deleted", func() {
			dpu := dpuObj(defaultDPUName)
			dpu.Status.Phase = provisioningv1.DPUConfig
			now := metav1.Now()
			dpu.DeletionTimestamp = &now

			status, err := state.DPUConfig(ctx, dpu, &dutil.ControllerContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Phase).To(Equal(provisioningv1.DPUDeleting))
		})
	})
})
