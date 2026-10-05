# dodai's fork of the EVS CSI plugin

Branch `dodai` on top of upstream `06c6079` (EVS v0.1.11). Only the EVS plugin is changed. Apache-2.0, like upstream.

Why fork: dodai runs databases on self-managed RKE2 (not CCE) in Huawei regions with no NAT. As shipped, the plugin strands a deleted ECS's VolumeAttachment (upstream #175), can't tag disks or set their enterprise project, needs a cloud key on every node, defaults to the global IAM endpoint (unreachable without NAT) and has no unit tests.

## Changes (one commit each)

1. Test seam: an interface over the EVS/ECS calls + an in-memory fake; table tests for every controller/node edge case.
2. **ControllerUnpublish never strands a volume** (#175): OK when the volume or the server is gone, or the volume isn't attached to that node — never while it is attached to a server that still exists.
3. ControllerPublish: busy on another node → `FailedPrecondition`.
4. CreateVolume/DeleteVolume: StorageClass `enterpriseProjectId` + `tags`; exact-name idempotency; `error`/`creating`/`deleting` handled; 10 GiB floor (the Cinder path is gone), 32 TiB cap; single-node-writer only; delete re-reads first.
5. Expand: rounded sizes, `OutOfRange` above the cap.
6. No `LIST_VOLUMES*` capabilities (unscoped by enterprise project).
7. Regional IAM endpoint by default.
8. **Keyless node**: no `--cloud-config` → node service only, device path resolved locally (`/dev/disk/by-id/virtio-…`), no cloud calls; a short-ID panic guard.
9. Per-volume lock (`Aborted` on a concurrent call).
10. Alpine 3.23 image (was CentOS 7).
11. (dodai.2) A detach whose job can't be followed (an empty job ID, seen live) waits on the volume instead of failing.

## Live-proven (Huawei Jakarta, 2026-10-05, `v0.1.11-dodai.2`)

Keyless node (zero cloud calls), regional IAM only, zone-aware provision with the enterprise project and tags, one disk after a controller killed mid-create, online expand 10→20 Gi under pgbench with 0 failed transactions, the stock image's #175 leak reproduced and released by the fork, the same disk re-attached with identical data across three node losses. Details: `docs/huawei-spike.md` in the dodai repo.

Manifests, pins and the StorageClass live in dodai (`deploy/rke2/evs-csi/`). Build: `GOOS=linux GOARCH=amd64 VERSION=<tag> REGISTRY=<registry> make image-evs-csi-plugin`.
