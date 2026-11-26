#!/usr/bin/env bash
set -euo pipefail
verify_only=false
if [[ "${1:-}" == --verify-only ]]; then
    verify_only=true
    shift
fi
scope=$(cd -- "$(dirname -- "$0")" && pwd -P)
repo=$(cd "${QUALIFICATION_REPO:?missing checkout}" && pwd -P)
build=$(cd "${1:?missing build directory}" && pwd -P)
[[ "$build" == "$scope"/run.* ]]
test "$(git -C "$repo" rev-parse --show-toplevel)" = "$repo"
base=0a6b143ad667c84f0e517b9ef0a9e9d5bd6ec367
base_tree=c12a0bf33f9bd39da85ba6a199ac9d76e16fa00b
candidate_tree=da71e398ce07b0b698840c80c20415fa496f3cc9
module=packages/kotlin
kotlin_tree=58cef09a6f151f83b9b4bd34e166d32646891145
candidate_kotlin_tree=4e0ac682c2657617fbb18f005a10e552497518a3
transport=src/main/kotlin/com/luxo/client/Transport.kt
tests=src/test/kotlin/com/luxo/client/HttpProtocolTest.kt
base_hash=2e6958e481632aa9e5ecba5b63baa29704f1dfa5d5918c8d6358b182169194ee
head_hash=cdebb326d3dfd0737b085fd592fc4862359c5869e39bbdf1b147001899f16937
base_tests_hash=1cae80c2c0382656e18db121975ea28e12319011946c06a387fe1651042f5460
head_tests_hash=b951f76b2997cf34cf5d53453f0a03a559169e8d3f3f3e2e02371790565c27cf
patch_hash=311290588f794f2a559dfd4e2861791f8690226eedff44bac67c7e26c23ff6c3

test "$(git -C "$repo" rev-parse "$base^{commit}")" = "$base"
test "$(git -C "$repo" rev-parse "$base^{tree}")" = "$base_tree"
test "$(git -C "$repo" rev-parse "$base:$module")" = "$kotlin_tree"
test "$(git -C "$repo" rev-parse "$base:VERSION")" = ffbc993986414defe0d2267186975c5dea8ea2e6
base64 -d < "$scope/transport.patch.b64" > "$build/product.diff"
printf '%s  %s\n' "$patch_hash" "$build/product.diff" | sha256sum -c -
(
    export GIT_INDEX_FILE="$build/product.index"
    test ! -e "$GIT_INDEX_FILE"
    git -C "$repo" read-tree "$base"
    git -C "$repo" apply --cached --whitespace=error "$build/product.diff"
    test "$(git -C "$repo" write-tree)" = "$candidate_tree"
    test "$(git -C "$repo" rev-parse "$candidate_tree:$module")" = "$candidate_kotlin_tree"
    diff -u <(printf '%s\n' "$module/$transport" "$module/$tests") \
        <(git -C "$repo" diff --cached --name-only "$base")
    git -C "$repo" apply --cached -R --whitespace=error "$build/product.diff"
    test "$(git -C "$repo" write-tree)" = "$base_tree"
)
test "$(git -C "$repo" show "$base:$module/$transport" | sha256sum | cut -d' ' -f1)" = "$base_hash"
test "$(git -C "$repo" show "$candidate_tree:$module/$transport" | sha256sum | cut -d' ' -f1)" = "$head_hash"
test "$(git -C "$repo" show "$base:$module/$tests" | sha256sum | cut -d' ' -f1)" = "$base_tests_hash"
test "$(git -C "$repo" show "$candidate_tree:$module/$tests" | sha256sum | cut -d' ' -f1)" = "$head_tests_hash"
{
    printf 'baseline=%s\nbase_tree=%s\nkotlin_tree=%s\n' "$base" "$base_tree" "$kotlin_tree"
    printf 'candidate_tree=%s\ncandidate_kotlin_tree=%s\n' "$candidate_tree" "$candidate_kotlin_tree"
    printf 'baseline_transport=%s\ncandidate_transport=%s\n' "$base_hash" "$head_hash"
    printf 'baseline_tests=%s\ncandidate_tests=%s\n' "$base_tests_hash" "$head_tests_hash"
    printf 'product_patch=%s\n' "$patch_hash"
} > "$build/product-binding.txt"
if "$verify_only"; then
    cat "$build/product-binding.txt"
    exit 0
fi

jdk=${JAVA_HOME:?missing pinned JAVA_HOME}
test "$(uname -sm)" = 'Linux x86_64'
java_version=$("$jdk/bin/java" -version 2>&1)
grep -Fx 'openjdk version "26.0.1" 2026-04-21' <<< "$java_version"
grep -Fx 'OpenJDK Runtime Environment Temurin-26.0.1+8 (build 26.0.1+8)' <<< "$java_version"
{
    printf 'baseline=%s\nbaseline_transport=%s\ncandidate_transport=%s\n' "$base" "$base_hash" "$head_hash"
    printf 'selected_java=%s\n' "$jdk/bin/java"
    uname -sm
    printf '%s\n' "$java_version"
    tail -n +2 "$build/product-binding.txt" | grep -v '^[a-z]*_transport='
    sha256sum "$scope"/{HttpSingleFlightBenchmark.kt,classpath.init.gradle,prepare.sh,qualify.py,runtime-sha256.tsv,transport.patch.b64} \
        "$jdk/bin/java" "$jdk/lib/server/libjvm.so"
} > "$build/build-metadata.txt"

mkdir "$build/gradle-home"
for role in base candidate; do
    arm="$build/$role"
    mkdir -p "$arm/system-tmp"
    tree=$base
    expected_transport=$base_hash
    gradle_mode=()
    if [[ "$role" == candidate ]]; then
        tree=$candidate_tree
        expected_transport=$head_hash
        gradle_mode=(--offline)
    fi
    git -C "$repo" archive "$tree" VERSION "$module/build.gradle.kts" "$module/settings.gradle.kts" \
        "$module/gradlew" "$module/gradle" "$module/gradle-plugin" "$module/src/main" |
        tar -x -C "$arm"
    sdk="$arm/$module"
    test "$(sha256sum "$sdk/$transport" | cut -d' ' -f1)" = "$expected_transport"
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
