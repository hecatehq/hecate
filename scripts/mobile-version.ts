const releaseVersionPattern = /^(\d+)\.(\d+)\.(\d+)(?:-(?:[A-Za-z]+)\.(\d+))?$/;

export function windowsVersion(semver: string): string {
  const match = semver.match(releaseVersionPattern);
  if (!match) {
    throw new Error(
      `unparseable semver for Windows MSI version: ${semver}. Expected M.m.p or M.m.p-<id>.N.`,
    );
  }
  const [, major, minor, patch, prerelease] = match;
  return `${major}.${minor}.${patch}.${prerelease ?? "0"}`;
}

export function appleBuildNumber(semver: string): string {
  const match = semver.match(releaseVersionPattern);
  if (!match) {
    throw new Error(
      `unparseable semver for Apple build number: ${semver}. Expected M.m.p or M.m.p-<id>.N.`,
    );
  }
  const [, major, minor, patch, prerelease] = match;
  const majorNumber = BigInt(major);
  const minorNumber = BigInt(minor);
  const patchNumber = BigInt(patch);
  const sequence = BigInt(prerelease ?? "99");

  // Apple permits three numeric components with maximum lengths of 4/2/2
  // digits and requires the first component to be greater than zero. Fold the
  // SemVer major/minor pair into the first component, keep patch in the
  // second, and reserve 99 in the third for the final release.
  if (minorNumber > 999n || patchNumber > 99n || (prerelease !== undefined && sequence > 98n)) {
    throw new Error(`Apple build components exceed the supported 4/2/2 digit encoding: ${semver}`);
  }
  const releaseLine = majorNumber * 1_000n + minorNumber + 1n;
  if (releaseLine > 9_999n) {
    throw new Error(`Apple build components exceed the supported 4/2/2 digit encoding: ${semver}`);
  }
  return `${releaseLine}.${patchNumber}.${sequence}`;
}

export function androidVersionCode(semver: string): number {
  const match = semver.match(releaseVersionPattern);
  if (!match) {
    throw new Error(
      `unparseable semver for Android version code: ${semver}. Expected M.m.p or M.m.p-<id>.N.`,
    );
  }
  const [, major, minor, patch, prerelease] = match;
  const minorNumber = BigInt(minor);
  const patchNumber = BigInt(patch);
  const sequence = BigInt(prerelease ?? "99");
  if (minorNumber > 999n || patchNumber > 999n || (prerelease !== undefined && sequence > 98n)) {
    throw new Error(`Android version components exceed the supported encoding: ${semver}`);
  }
  const code =
    BigInt(major) * 100_000_000n + minorNumber * 100_000n + patchNumber * 100n + sequence;
  if (code < 1n || code > 2_100_000_000n) {
    throw new Error(`Android version code is outside Google Play's supported range: ${semver}`);
  }
  return Number(code);
}
