# alpine:3.23, pinned by digest (centos:7 is end of life)
FROM docker.io/library/alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0
LABEL maintainers="Huawei Cloud Authors"
LABEL description="Huawei Cloud EVS CSI Plugin"

# util-linux: real mount/umount/blkid/findmnt/blockdev (not busybox's) for mount-utils;
# e2fsprogs(-extra): mkfs.ext4, resize2fs, dumpe2fs; xfsprogs(-extra): mkfs.xfs, xfs_growfs
RUN apk add --no-cache ca-certificates util-linux e2fsprogs e2fsprogs-extra xfsprogs xfsprogs-extra

COPY evs-csi-plugin /bin/evs-csi-plugin

ENTRYPOINT ["/bin/evs-csi-plugin"]
