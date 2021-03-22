// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	tsuruv1 "github.com/tsuru/tsuru/provision/kubernetes/pkg/apis/tsuru/v1"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	"github.com/tsuru/tsuru/types/provision"
	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetWebService(t *testing.T) {
	svc := BaseService{
		Namespace:        "default",
		Client:           fake.NewSimpleClientset(),
		TsuruClient:      faketsuru.NewSimpleClientset(),
		ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
	}

	_, err := svc.getWebService(ctx, "test", router.BackendTarget{Service: "test-not-found", Namespace: svc.Namespace})
	assert.Equal(t, ErrNoService{App: "test"}, err)

	svc1 := v1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      "test-single",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "test"},
	},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-single"},
			Ports:    []v1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899)}},
		},
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(ctx, &svc1, metav1.CreateOptions{})
	require.NoError(t, err)
	webService, err := svc.getWebService(ctx, "test", router.BackendTarget{Service: svc1.Name, Namespace: svc1.Namespace})
	require.NoError(t, err)
	assert.Equal(t, "test-single", webService.Name)

	err = createCRD(&svc, "namespacedApp", "custom-namespace", nil)
	require.NoError(t, err)

	svc3 := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "namespacedApp-web",
			Namespace: "custom-namespace",
			Labels:    map[string]string{appLabel: "namespacedApp", processLabel: "web"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "namespacedApp-web"},
			Ports:    []v1.ServicePort{{Protocol: "TCP", Port: int32(8890), TargetPort: intstr.FromInt(8890)}},
		},
	}
	_, err = svc.Client.CoreV1().Services(svc3.Namespace).Create(ctx, &svc3, metav1.CreateOptions{})
	require.NoError(t, err)

	webService, err = svc.getWebService(ctx, "namespacedApp", router.BackendTarget{Service: svc3.Name, Namespace: svc3.Namespace})
	require.NoError(t, err)
	assert.Equal(t, "namespacedApp-web", webService.Name)
}

func createCRD(svc *BaseService, app string, namespace string, configs *provision.TsuruYamlKubernetesConfig) error {
	_, err := svc.ExtensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(ctx, &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "apps.tsuru.io"},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   "tsuru.io",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Plural:   "apps",
				Singular: "app",
				Kind:     "App",
				ListKind: "AppList",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = svc.TsuruClient.TsuruV1().Apps(svc.Namespace).Create(ctx, &tsuruv1.App{
		ObjectMeta: metav1.ObjectMeta{Name: app},
		Spec: tsuruv1.AppSpec{
			NamespaceName: namespace,
			Configs:       configs,
		},
	}, metav1.CreateOptions{})
	return err
}

func idForApp(appName string) router.InstanceID {
	return router.InstanceID{AppName: appName}
}
