import json
import os
from pathlib import Path
import platform
import re
import secrets
import subprocess
import time

IMAGE = "docker.io/library/postgres@sha256:e84240fdc0f11ac4cff65865db840555aa09c02ae532eb4c95822881164da0e4"
IMAGE_ID = "sha256:2e8431cb291f0a0ac50f815acf33e593857dec572dc08e330f666930678ec565"
OWNER_LABEL = "io.luxo.pg-durable.job"
DATA_DIR = "/var/lib/postgresql/data"
SETTINGS = {
    "fsync": "on", "synchronous_commit": "on",
    "shared_buffers": "501376kB", "checkpoint_timeout": "5min", "max_wal_size": "1GB",
}


def capture(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def docker(*args, **kwargs):
    return capture("docker", "--host", "unix:///var/run/docker.sock", *args, **kwargs)


def scope():
    required = {
        "GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "light-speak/luxo",
        "GITHUB_REF": "refs/heads/diagnostics/hosted-pg-durable-0a6b",
        "RUNNER_ENVIRONMENT": "github-hosted", "RUNNER_OS": "Linux", "RUNNER_ARCH": "ARM64",
    }
    if platform.system() != "Linux" or platform.machine() != "aarch64":
        raise RuntimeError("expected native ARM64 Linux")
    if any(os.environ.get(key) != value for key, value in required.items()):
        raise RuntimeError("expected the dedicated GitHub-hosted Linux ARM64 diagnostic job")
    run_id, attempt = (os.environ[key] for key in ("GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"))
    if not all(re.fullmatch(r"[0-9]{1,20}", x) for x in (run_id, attempt)):
        raise RuntimeError("unexpected GitHub attempt identity")
    name = f"luxo-pg-durable-{run_id}-{attempt}"
    return name, f"{name}-data"


def admin(container, sql):
    return docker(
        "exec", "-i", container, "sh", "-c",
        'exec psql -X -Atq -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres',
        input=sql + "\n", stderr=subprocess.PIPE,
    )


def owned_resources():
    name, volume = scope()
    containers = docker("ps", "-a", "--filter", f"name=^/{name}$", "--no-trunc", "--format", "{{.ID}}")
    volumes = docker("volume", "ls", "--filter", f"name=^{volume}$", "--format", "{{.Name}}")
    return containers.splitlines(), volumes.splitlines()


def remove_owned():
    name, volume = scope()
    containers, volumes = owned_resources()
    if len(containers) > 1 or volumes not in ([], [volume]):
        raise RuntimeError("unexpected PostgreSQL resource inventory")
    if containers:
        owner = docker("inspect", "--format", '{{index .Config.Labels "' + OWNER_LABEL + '"}}', containers[0])
        if owner != name:
            raise RuntimeError("refusing removal of a PostgreSQL container outside this job")
        docker("rm", "-f", containers[0])
    if volumes:
        owner = docker("volume", "inspect", "--format", '{{index .Labels "' + OWNER_LABEL + '"}}', volume)
        if owner != name:
            raise RuntimeError("refusing removal of a PostgreSQL volume outside this job")
        docker("volume", "rm", volume)
    check = tuple(map(len, owned_resources()))
    if check != (0, 0):
        raise RuntimeError("owned PostgreSQL container or volume remains")
    return {"containers": check[0], "volumes": check[1]}


def verify_server():
    name, volume = scope()
    container = json.loads(docker("inspect", "--format", "{{json .}}", name))
    if (container["Name"], container["Image"], container["Config"]["Image"]) != (f"/{name}", IMAGE_ID, IMAGE):
        raise RuntimeError("PostgreSQL container or image identity changed")
    if not container["State"]["Running"] or container["Config"]["Labels"].get(OWNER_LABEL) != name:
        raise RuntimeError("PostgreSQL container is stopped or outside this job")
    mounted_data = [(m["Type"], m.get("Name"), m["RW"]) for m in container["Mounts"] if m["Destination"] == DATA_DIR]
    if mounted_data != [("volume", volume, True)]:
        raise RuntimeError("unexpected PostgreSQL data mount")
    if docker("port", name, "5432/tcp") != "127.0.0.1:5432":
        raise RuntimeError("unexpected PostgreSQL loopback port")
    query = ("server_version_num", "server_version", *SETTINGS)
    sql = "SELECT " + ", ".join(f"current_setting('{setting}')" for setting in query) + ";"
    version_num, version, *settings = admin(name, sql).split("|")
    if version_num != "160014" or settings != list(SETTINGS.values()):
        raise RuntimeError(f"unexpected PostgreSQL build or settings: {version_num}, {settings}")
    guest = docker("exec", name, "uname", "-srm")
    if not guest.endswith(" aarch64"):
        raise RuntimeError("PostgreSQL container is not native ARM64")
    cpu = capture("lscpu", "-J")
    return {
        "protocol": "github-ubuntu-24.04-arm-postgres-16.14-bookworm",
        "container": container["Id"], "image": IMAGE, "imageConfig": IMAGE_ID,
        "guest": guest, "host": list(platform.uname()), "cpu": json.loads(cpu),
        "serverVersion": version, "serverVersionNum": version_num, "settings": SETTINGS,
        "volume": volume, "runnerImage": os.environ.get("ImageVersion"),
    }


def start_owned():
    name, volume = scope()
    if docker("info", "--format", "{{.OSType}}/{{.Architecture}}") != "linux/aarch64":
        raise RuntimeError("expected native ARM64 Docker daemon")
    if owned_resources() != ([], []):
        raise RuntimeError("existing diagnostic container or volume needs accounting")
    docker("pull", "--platform=linux/arm64", IMAGE)
    record = docker("image", "inspect", "--format", "{{.Id}}|{{.Os}}|{{.Architecture}}", IMAGE)
    if record != f"{IMAGE_ID}|linux|arm64":
        raise RuntimeError("pulled PostgreSQL manifest or image config does not match")
    docker("volume", "create", "--label", f"{OWNER_LABEL}={name}", volume)
    params = [item for key, value in SETTINGS.items() for item in ("-c", f"{key}={value}")]
    docker(
        "run", "-d", "--pull=never", "--name", name, "--label", f"{OWNER_LABEL}={name}", "--platform", "linux/arm64",
        "--publish", "127.0.0.1:5432:5432", "--shm-size", "1g",
        "--mount", f"type=volume,source={volume},target={DATA_DIR}",
        "--env", "POSTGRES_USER=postgres", "--env", "POSTGRES_PASSWORD", IMAGE, *params,
        env={**os.environ, "POSTGRES_PASSWORD": secrets.token_hex(32)},
    )
    for _ in range(45):
        probe = subprocess.run(
            ["docker", "--host", "unix:///var/run/docker.sock", "exec", name, "pg_isready",
             "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        if probe.returncode == 0:
            return verify_server()
        time.sleep(2)
    raise RuntimeError("owned PostgreSQL failed to accept TCP connections before the 90s deadline")
