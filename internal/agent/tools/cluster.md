Discover and use the other machines in this Crush cluster. This tool only appears while this server is paired into a cluster (`crush cluster status`). Use it to fan work out across machines.

Actions:

- `cluster({ action: "nodes" })`: lists every member node: name, short id, online/offline, host, CPU/memory load, a spare-capacity score (`free`, higher is roomier), round-trip time, and the sessions currently running a turn on each node as `color-animal` addresses.
- `cluster({ action: "spawn", node: "mini1", path: "/Users/me/code/proj", prompt: "..." })`: creates a new session on that node in the workspace rooted at `path`, then sends `prompt` as its first message. `node` accepts a node name, node id, or id prefix. Use `"auto"` to pick the online node with the most spare capacity. Optional `title` and `model`; the model is resolved in the target node's config.

Notes:

- `path` is resolved on the TARGET node and must already exist there. Home directories and checkouts often differ between machines; if the path is missing the spawn fails with a clear error. Code is NOT copied. If the project is not on that node, have the spawned session clone it (spawn it in an existing directory such as the user's home and tell it to clone), or move an existing session with `crush session move <session> <node>`, which copies the project files.
- Sessions on every node are addressable with the `swarm` tool by their `color-animal` address, exactly like local ones. Lookup searches the whole cluster.
- There is no automatic reply. If you need results back, tell the spawned session in its prompt to reply via `swarm` to your own address.
- For many independent tasks, spawn one session per task and spread them across nodes (or use `node: "auto"` repeatedly) instead of sending everything to one node.
--