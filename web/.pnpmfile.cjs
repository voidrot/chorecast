// TypeScript 7 (the native compiler) is this project's `tsc`, but vue-tsc/Volar and
// openapi-typescript still use the TS 6 JavaScript API, which TS 7 doesn't ship.
// Give those packages a private TS 6 instead of the project's TS 7 peer.
// Remove once they support TS 7.
const TS6 = '~6.0.3'
const NEEDS_TS6 = new Set([
  'vue-tsc',
  '@vue/language-core',
  '@volar/typescript',
  '@volar/language-service',
  'openapi-typescript',
])

module.exports = {
  hooks: {
    readPackage(pkg) {
      if (NEEDS_TS6.has(pkg.name)) {
        if (pkg.peerDependencies) delete pkg.peerDependencies.typescript
        if (pkg.peerDependenciesMeta) delete pkg.peerDependenciesMeta.typescript
        pkg.dependencies = { ...pkg.dependencies, typescript: TS6 }
      }
      return pkg
    },
  },
}
