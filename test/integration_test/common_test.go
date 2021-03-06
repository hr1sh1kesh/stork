// +build integrationtest

package integrationtest

import (
	"testing"

	storkdriver "github.com/libopenstorage/stork/drivers/volume"
	_ "github.com/libopenstorage/stork/drivers/volume/portworx"
	"github.com/portworx/torpedo/drivers/node"
	_ "github.com/portworx/torpedo/drivers/node/ssh"
	"github.com/portworx/torpedo/drivers/scheduler"
	_ "github.com/portworx/torpedo/drivers/scheduler/k8s"
	"github.com/portworx/torpedo/drivers/volume"
	_ "github.com/portworx/torpedo/drivers/volume/portworx"
	"github.com/sirupsen/logrus"
	"github.com/skyrings/skyring-common/tools/uuid"
	"github.com/stretchr/testify/require"
)

const (
	nodeDriverName      = "ssh"
	volumeDriverName    = "pxd"
	schedulerDriverName = "k8s"
)

var nodeDriver node.Driver
var schedulerDriver scheduler.Driver
var volumeDriver volume.Driver
var storkVolumeDriver storkdriver.Driver

// TODO: Start stork scheduler and stork extender
// TODO: Take driver name from input
// TODO: Parse storageclass specs based on driver name
func setup(t *testing.T) {
	var err error

	storkVolumeDriver, err = storkdriver.Get(volumeDriverName)
	require.NoError(t, err, "Error getting stork driver %v", volumeDriverName)

	err = storkVolumeDriver.Init(nil)
	require.NoError(t, err, "Error initializing stork driver %v", volumeDriverName)

	nodeDriver, err = node.Get(nodeDriverName)
	require.NoError(t, err, "Error getting node driver %v", nodeDriverName)

	err = nodeDriver.Init()
	require.NoError(t, err, "Error initializing node driver %v", nodeDriverName)

	schedulerDriver, err = scheduler.Get(schedulerDriverName)
	require.NoError(t, err, "Error getting scheduler driver %v", schedulerDriverName)

	err = schedulerDriver.Init("/specs", volumeDriverName, nodeDriverName)
	require.NoError(t, err, "Error initializing scheduler driver %v", schedulerDriverName)

	volumeDriver, err = volume.Get(volumeDriverName)
	require.NoError(t, err, "Error getting volume driver %v", volumeDriverName)

	err = volumeDriver.Init(schedulerDriverName, nodeDriverName)
	require.NoError(t, err, "Error initializing volume driver %v", volumeDriverName)
}

func TestMain(t *testing.T) {
	// If setup fails stop the test
	if passed := t.Run("setup", setup); !passed {
		t.FailNow()
	}
	t.Run("Extender", testExtender)
	t.Run("HealthMonitor", testHealthMonitor)
	t.Run("Snapshot", testSnapshot)
}

func generateInstanceID(t *testing.T, testName string) string {
	id, err := uuid.New()
	require.NoError(t, err, "Error generating uuid for task")
	return testName + "-" + id.String()
}

func destroyAndWait(t *testing.T, ctxs []*scheduler.Context) {
	for _, ctx := range ctxs {
		err := schedulerDriver.Destroy(ctx, nil)
		require.NoError(t, err, "Error destroying ctx: %+v", ctx)
		err = schedulerDriver.WaitForDestroy(ctx)
		require.NoError(t, err, "Error waiting for destroy of ctx: %+v", ctx)
		_, err = schedulerDriver.DeleteVolumes(ctx)
		require.NoError(t, err, "Error deleting volumes in ctx: %+v", ctx)
	}
}

func getVolumeNames(t *testing.T, ctx *scheduler.Context) []string {
	volumeParams, err := schedulerDriver.GetVolumeParameters(ctx)
	require.NoError(t, err, "Error getting volume Parameters")

	var volumes []string
	for vol := range volumeParams {
		volumes = append(volumes, vol)
	}
	return volumes
}

func verifyScheduledNode(t *testing.T, appNode node.Node, volumes []string) {
	driverNodes, err := storkVolumeDriver.GetNodes()
	require.NoError(t, err, "Error getting nodes from stork driver")

	found := false
	for _, dNode := range driverNodes {
		if dNode.ID == appNode.Name {
			found = true
			break
		}
	}
	require.Equal(t, true, found, "Scheduled node not found in driver node list")

	scores := make(map[string]int)
	for _, dNode := range driverNodes {
		scores[dNode.ID] = 0
	}

	highScore := 0
	// Calculate scores for each node
	for _, vol := range volumes {
		volInfo, err := storkVolumeDriver.InspectVolume(vol)
		require.NoError(t, err, "Error inspecting volume %v", vol)

		for _, dataNode := range volInfo.DataNodes {
			scores[dataNode] = scores[dataNode] + 10
			if scores[dataNode] > highScore {
				highScore = scores[dataNode]
			}
		}
	}

	logrus.Debugf("Scores: %v", scores)
	require.Equal(t, highScore, scores[appNode.Name], "Scheduled node does not have the highest score")
}
