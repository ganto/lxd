---
relatedlinks: "[How&#32;to&#32;install&#32;a&#32;Windows&#32;11&#32;VM&#32;using&#32;LXD&#32;|&#32;Ubuntu](https://ubuntu.com/tutorials/how-to-install-a-windows-11-vm-using-lxd)"
---

(images-create)=
# How to create images

If you want to create and share your own images, you can do this either based on an existing instance or snapshot or by building your own image from scratch.

(images-create-publish)=
## Publish an image from an instance or snapshot

If you want to be able to use an instance or an instance snapshot as the base for new instances, you should create and publish an image from it.

To publish an image from an instance, make sure that the instance is stopped.
Then enter the following command:

    lxc publish <instance_name> [<remote>:]

To publish an image from a snapshot, enter the following command:

    lxc publish <instance_name>/<snapshot_name> [<remote>:]

In both cases, you can specify an alias for the new image with the `--alias` flag, set an expiration date with `--expire` and make the image publicly available with `--public`.
If an image with the same name already exists, add the `--reuse` flag to overwrite it.
See [`lxc publish --help`](lxc_publish.md) for a full list of available flags.

The publishing process can take quite a while because it generates a tarball from the instance or snapshot and then compresses it.
As this can be particularly I/O and CPU intensive, publish operations are serialized by LXD.

### Prepare the instance for publishing

Before you publish an image from an instance, clean up all data that should not be included in the image.
Usually, this includes the following data:

- Instance metadata (use [`lxc config metadata`](lxc_config_metadata.md) to edit)
- File templates (use [`lxc config template`](lxc_config_template.md) to edit)
- Instance-specific data inside the instance itself (for example, host SSH keys and `dbus/systemd machine-id`)

(images-create-build)=
## Build an image

For building your own images, you can use [`distrobuilder`](https://github.com/lxc/distrobuilder).

See the [`distrobuilder` documentation](https://linuxcontainers.org/distrobuilder/docs/latest/) for instructions for installing and using the tool.
