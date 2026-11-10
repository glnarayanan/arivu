# Inspect graph

Graph shows a bounded map of typed nodes and edges, with an inspector, zoom controls, and an equivalent Accessible node list.

## Sub-features

- `graph-open` opens `/graph` from Primary nav.
- `graph-list` exposes the same nodes as the canvas via `Accessible node list`.
- `graph-controls` shows Zoom out, Reset view, and Zoom in.
- `graph-empty` is valid on a fresh instance with no derived neighborhood.

## How to get to it (user POV)

- Choose `Graph` in the Primary nav.
- Open `/graph` directly.
- Compatibility: `/knowledge-graph` redirects to Graph.

## Driving it with control-arivu

Preconditions:

- Isolated instance is healthy and the verify user is signed in.
- Notes or bookmarks may be absent. An empty graph is an allowed end state for this recipe.

- **Open Graph.** Choose `Graph`. `#route-title` reads `Graph`. Copy mentions a focused, bounded view. Label `Focus node` is present.
- **Controls.** Group `Graph view controls` contains `Zoom out`, `Reset view`, and `Zoom in`. Region `Interactive knowledge graph` is present.
- **Accessible list.** `Accessible node list` is open (or can be opened). It states that the list contains the same nodes as the visual graph. On a fresh database the list may have no node links; that is success if the list region exists.
- **Compatibility.** Open `/knowledge-graph`. The visible destination is Graph (`#route-title` `Graph`), not a separate product.
- **Proof.** Save `artifacts/$RUN_ID/graph-inspect/graph.aria.txt` and `graph.png`. Both identify Arivu, heading `Graph`, `Accessible node list`, and the zoom controls.

## Gotchas

- Do not fail the recipe because there are zero nodes. Insights and Graph on a new account are often empty.
- Do not require a model provider or embeddings. Explicit structure still renders when present.
- The canvas is not the only access path. If SVG nodes are hard to drive, use the Accessible node list.
- A proof that only screenshots a blank canvas without the heading or list is incomplete.
