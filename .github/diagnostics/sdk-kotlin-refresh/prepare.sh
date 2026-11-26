#!/usr/bin/env bash
set -euo pipefail
scope=$(cd -- "$(dirname -- "$0")" && pwd -P)
repo=$(cd "${QUALIFICATION_REPO:?missing checkout}" && pwd -P)
build=$(cd "${1:?missing build directory}" && pwd -P)
[[ "$build" == "$scope"/run.* ]]
jdk=${JAVA_HOME:?missing pinned JAVA_HOME}
base=77b6cdf20d424213f4c1dc90534f924ac6b176d0
base_tree=9014a2b7a92f5b2abbb5aca10a6a9a7d0978955f
module=packages/kotlin
kotlin_tree=9f47b2309da48711302cc48d8bc937bd41137e57
transport=src/main/kotlin/com/luxo/client/Transport.kt
base_hash=33d5d22886da79b5bca2267da4eda63423be78aeab542272b38e514d2f36a556
head_hash=2e6958e481632aa9e5ecba5b63baa29704f1dfa5d5918c8d6358b182169194ee

test "$(uname -sm)" = 'Linux x86_64'
java_version=$("$jdk/bin/java" -version 2>&1)
grep -Fx 'openjdk version "26.0.1" 2026-04-21' <<< "$java_version"
grep -Fx 'OpenJDK Runtime Environment Temurin-26.0.1+8 (build 26.0.1+8)' <<< "$java_version"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" rev-parse "$base:$module")" = "$kotlin_tree"
test "$(git -C "$repo" rev-parse "$base:VERSION")" = ffbc993986414defe0d2267186975c5dea8ea2e6
{
    printf 'baseline=%s\nbaseline_transport=%s\ncandidate_transport=%s\n' "$base" "$base_hash" "$head_hash"
    printf 'selected_java=%s\n' "$jdk/bin/java"
    uname -sm
    printf '%s\n' "$java_version"
    printf 'base_tree=%s\nkotlin_tree=%s\n' "$base_tree" "$kotlin_tree"
    printf 'candidate_tree=%s\n' 5b692e993d08e4d85739f70cd8bb8bf57301f228
    sha256sum "$scope"/{HttpSingleFlightBenchmark.kt,classpath.init.gradle,qualify.py,runtime-sha256.tsv,transport.patch.b64} \
        "$jdk/bin/java" "$jdk/lib/server/libjvm.so"
} > "$build/build-metadata.txt"

base64 -d "$scope/transport.patch.b64" > "$build/transport.diff"
printf '%s  %s\n' 86be2412b0f3801d8378f369e7f5fad172dd75438e27e9c50aee6404639bf49d "$build/transport.diff" | sha256sum -c -
mkdir "$build/gradle-home"
for role in base candidate; do
    arm="$build/$role"
    mkdir -p "$arm/system-tmp"
    git -C "$repo" archive "$base" VERSION "$module/build.gradle.kts" "$module/settings.gradle.kts" \
        "$module/gradlew" "$module/gradle" "$module/gradle-plugin" "$module/src/main" |
        tar -x -C "$arm"
    sdk="$arm/$module"
    test "$(sha256sum "$sdk/$transport" | cut -d' ' -f1)" = "$base_hash"
    gradle_mode=()
    if [[ "$role" == candidate ]]; then
        GIT_CEILING_DIRECTORIES="$build" git -C "$arm" apply --check --whitespace=error "$build/transport.diff"
        GIT_CEILING_DIRECTORIES="$build" git -C "$arm" apply --whitespace=error "$build/transport.diff"
        test "$(sha256sum "$sdk/$transport" | cut -d' ' -f1)" = "$head_hash"
        gradle_mode=(--offline)
    fi
    grep -Fqx 'kotlin = "2.4.20"' "$sdk/gradle/libs.versions.toml"
    grep -Fqx 'distributionUrl=https\://services.gradle.org/distributions/gradle-9.7.1-bin.zip' "$sdk/gradle/wrapper/gradle-wrapper.properties"
    grep -Fqx 'distributionSha256Sum=acd53f1edaf02f1a8ff99879f8a34b302661a057d9b063ae9e35b552f804d20a' "$sdk/gradle/wrapper/gradle-wrapper.properties"
    test "$(sha256sum "$sdk/gradle/wrapper/gradle-wrapper.jar" | cut -d' ' -f1)" = 7a9ce74cff467ca1bf60a4fcd9f05185acceda4d0f382434d393e17864262c5d
    mkdir -p "$sdk/src/test/kotlin/com/luxo/client"
    cp "$scope/HttpSingleFlightBenchmark.kt" "$sdk/src/test/kotlin/com/luxo/client/"
    (
        cd "$sdk"
        export TMPDIR="$arm/system-tmp" GRADLE_USER_HOME="$build/gradle-home"
        export GRADLE_OPTS="-Xmx128m -XX:ActiveProcessorCount=2 -XX:-UsePerfData -Djava.io.tmpdir=$TMPDIR"
        ./gradlew "${gradle_mode[@]}" --no-daemon --no-watch-fs --console=plain \
            --project-cache-dir "$arm/project-cache" --max-workers=1 \
            -Pkotlin.compiler.execution.strategy=in-process \
            '-Dorg.gradle.jvmargs=-Xms64m -Xmx768m -XX:MaxMetaspaceSize=384m -XX:ActiveProcessorCount=2 -XX:-UsePerfData -Dfile.encoding=UTF-8' \
            "-Dorg.gradle.java.home=$jdk" "-Djava.io.tmpdir=$TMPDIR" \
            "-Dluxo.auditClasspath=$arm/classpath.txt" -I "$scope/classpath.init.gradle" :writeHttpSingleFlightClasspath
    ) > "$scope/$role.gradle.log" 2>&1 || {
        tail -60 "$scope/$role.gradle.log" >&2
        exit 1
    }
    test -s "$arm/classpath.txt"
done
