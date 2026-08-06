const pinnedVersionPattern = "\\d+\\.\\d+\\.\\d+(?:-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?";
const dockerTagBoundary = "(?![0-9A-Za-z_.-])";

export function updatePinnedReleaseReferences(value: string, tag: string): string {
  const semver = tag.replace(/^v/, "");
  return value
    .replace(
      new RegExp(
        `^(\\s*(?:image:\\s+)?ghcr\\.io/hecatehq/hecate:)${pinnedVersionPattern}${dockerTagBoundary}(\\s*)$`,
        "gm",
      ),
      `$1${semver}$2`,
    )
    .replace(
      new RegExp(
        `(^|[^0-9A-Za-z./_-])(https://github\\.com/hecatehq/hecate/releases/download/)v${pinnedVersionPattern}/([Hh]ecate_)${pinnedVersionPattern}_`,
        "g",
      ),
      `$1$2${tag}/$3${semver}_`,
    )
    .replace(
      new RegExp(`^Available tarballs for \`v${pinnedVersionPattern}\`:$`, "gm"),
      `Available tarballs for \`${tag}\`:`,
    )
    .replace(
      new RegExp(`^## Current state — \`v${pinnedVersionPattern}\`$`, "gm"),
      `## Current state — \`${tag}\``,
    );
}
