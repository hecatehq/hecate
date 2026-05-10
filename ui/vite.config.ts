import type { Plugin } from "vite";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// preserveDistGitkeep emits ui/dist/.gitkeep alongside the build
// output. The file is the placeholder that lets
// `//go:embed all:ui/dist` (embed.go in the module root) compile
// against a fresh clone where `bun run build` hasn't run yet —
// without a file inside ui/dist/, Go's embed directive fails with
// "pattern all:ui/dist: no matching files found".
//
// Vite's default `emptyOutDir: true` wipes the directory before
// writing, so a tracked `.gitkeep` would disappear on every build
// and the worktree would show a phantom deletion. Emitting via
// Rollup's `emitFile` API runs as part of the normal bundle output,
// after empty-out-dir, so the file always lands in dist regardless
// of who invoked the build (Justfile, `bun run build` directly,
// CI without just, an IDE-triggered build). The Justfile used to
// restore it as a post-step; that's no longer needed.
function preserveDistGitkeep(): Plugin {
  return {
    name: "preserve-dist-gitkeep",
    apply: "build",
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: ".gitkeep",
        source: "",
      });
    },
  };
}

// preloadLikelyFirstWorkspace injects a `<link rel="modulepreload">`
// for the chunk we expect the operator to need first, so the browser
// fetches and parses it in parallel with the initial entry chunk
// instead of waiting until React.lazy resolves on first render.
//
// The "likely first workspace" is Chats — it's the runtime console's
// default landing surface for new operators, the most-used surface
// for repeat operators, and (incidentally) the largest workspace
// chunk (~91 KB / ~23 KB gzipped). Preloading any other workspace
// would help in narrower scenarios; preloading Chats is the broadest
// hit-rate.
//
// Cost when the operator's last-active workspace is something else
// (the app remembers via localStorage): ~23 KB gzipped of wasted
// bandwidth. On localhost / LAN — Hecate's deployment shape — this
// is invisible. On a hypothetical remote install it would be
// notable, but Hecate isn't deployed that way today.
//
// Browsers automatically follow the import graph for modulepreloaded
// chunks (Chrome, Firefox, Safari all do this), so ChatView's shared
// dependencies (AddProviderModal, TranscriptActivityTimeline, ui)
// get pulled along without explicit tags.
function preloadLikelyFirstWorkspace(): Plugin {
  return {
    name: "preload-likely-first-workspace",
    apply: "build",
    enforce: "post",
    transformIndexHtml: {
      order: "post",
      handler(_html, { bundle }) {
        if (!bundle) return;
        for (const fileName of Object.keys(bundle)) {
          if (/^assets\/ChatView-.*\.js$/.test(fileName)) {
            return [
              {
                tag: "link",
                attrs: { rel: "modulepreload", href: `/${fileName}` },
                injectTo: "head",
              },
            ];
          }
        }
      },
    },
  };
}

export default defineConfig({
  plugins: [react(), preloadLikelyFirstWorkspace(), preserveDistGitkeep()],
  server: {
    port: 5173,
    proxy: {
      "/healthz": "http://127.0.0.1:8765",
      "/hecate": "http://127.0.0.1:8765",
      "/v1": "http://127.0.0.1:8765",
    },
  },
});
