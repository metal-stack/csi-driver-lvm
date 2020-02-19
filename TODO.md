
pkg/lvm/controllerserver.go:34:2: `deviceID` is unused (deadcode)
        deviceID           = "deviceID"
        ^
pkg/lvm/controllerserver.go:41:2: `mountAccess` is unused (deadcode)
        mountAccess accessType = iota
        ^
pkg/lvm/controllerserver.go:42:2: `blockAccess` is unused (deadcode)
        blockAccess
        ^
pkg/lvm/lvm.go:44:2: `gib100` is unused (deadcode)
        gib100 int64 = gib * 100
        ^
pkg/lvm/lvm.go:46:2: `tib100` is unused (deadcode)
        tib100 int64 = tib * 100
        ^
pkg/lvm/lvm.go:88:2: `keyNode` is unused (deadcode)
        keyNode          = "kubernetes.io/hostname"
        ^
pkg/lvm/lvm.go:89:2: `typeAnnotation` is unused (deadcode)
        typeAnnotation   = "csi-lvm.metal-stack.io/type"
        ^
pkg/lvm/lvm.go:95:2: `pullAlways` is unused (deadcode)
        pullAlways       = "always"
        ^
cmd/provisioner/createlv.go:13:2: `linearType` is unused (deadcode)
        linearType  = "linear"
        ^
cmd/provisioner/createlv.go:14:2: `stripedType` is unused (deadcode)
        stripedType = "striped"
        ^
cmd/provisioner/createlv.go:15:2: `mirrorType` is unused (deadcode)
        mirrorType  = "mirror"
        ^
cmd/provisioner/main.go:16:2: `flagDirectory` is unused (deadcode)
        flagDirectory      = "directory"
        ^
pkg/lvm/controllerserver.go:38:6: type `accessType` is unused (unused)