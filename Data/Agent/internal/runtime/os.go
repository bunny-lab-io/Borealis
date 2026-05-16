package agentruntime

import "runtime"

func operatingSystemName() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
