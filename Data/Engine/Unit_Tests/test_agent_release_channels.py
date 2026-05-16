from __future__ import annotations

import zipfile

from Data.Engine.services.agent_release_channels import AgentReleaseChannelManager


def test_release_manager_packages_go_agent_binary_bundle(tmp_path) -> None:
    source = tmp_path / "source.zip"
    with zipfile.ZipFile(source, "w") as archive:
        archive.writestr("Borealis-main/Data/Agent/dist/windows-amd64/Agent.exe", b"MZwindows-agent")
        archive.writestr("Borealis-main/Data/Agent/dist/linux-amd64/Agent", b"\x7fELFlinux-agent")
        archive.writestr("Borealis-main/README.md", b"ignored")

    manager = AgentReleaseChannelManager.__new__(AgentReleaseChannelManager)
    destination = tmp_path / "bundle.zip"
    manager._package_go_agent_artifact(
        source_archive_path=source,
        destination_path=destination,
        manifest={"channel": "unstable", "build_id": "abc123"},
    )
    manager._validate_cached_artifact(destination)

    with zipfile.ZipFile(destination) as archive:
        names = set(archive.namelist())
        assert "manifest.json" in names
        assert "Data/Agent/dist/windows-amd64/Agent.exe" in names
        assert "Data/Agent/dist/linux-amd64/Agent" in names
        assert "Borealis-main/README.md" not in names


def test_release_manager_rejects_artifact_without_go_binaries(tmp_path) -> None:
    source = tmp_path / "source.zip"
    with zipfile.ZipFile(source, "w") as archive:
        archive.writestr("Borealis-main/README.md", b"missing binaries")

    manager = AgentReleaseChannelManager.__new__(AgentReleaseChannelManager)
    destination = tmp_path / "bundle.zip"

    try:
        manager._package_go_agent_artifact(
            source_archive_path=source,
            destination_path=destination,
            manifest={"channel": "unstable", "build_id": "abc123"},
        )
    except RuntimeError as exc:
        assert "prebuilt Go Agent binaries" in str(exc)
    else:
        raise AssertionError("missing Go Agent binaries should be rejected")
