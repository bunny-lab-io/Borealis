# Release Notes

Building on the PostgreSQL foundation introduced in [Borealis 2026.03.2](https://github.com/bunny-lab-io/Borealis/releases/tag/2026.03.2), Borealis now integrates with the [Aurora Repository](https://github.com/bunny-lab-io/Aurora) as the decentralized source of truth for official assemblies, workflows, scripts, and playbooks.  Aurora was created so official automation content could evolve on its own lifecycle, outside the main Borealis codebase, while still being easy for operators to discover, organize, version, and update.  

- Aurora was created to solve a practical problem: official assemblies often need to be improved, corrected, reorganized, or expanded more frequently than the core engine itself, and those changes should not require a full Borealis code release every time.  
- By moving official automation content into Aurora, Borealis keeps the main codebase focused on platform logic while giving assemblies, workflows, scripts, and playbooks their own dedicated home, their own Git history, their own organizational structure, and a cleaner maintenance workflow.  
- Aurora is intended to be the source for official assemblies only, while user-created assemblies continue to live inside the Borealis Engine PostgreSQL database so operators can still build and manage their own local automation content inside the product.  
- Borealis can check Aurora for updates and ingest those official assembly changes into PostgreSQL, making them available to operators and scheduled jobs through the concurrency and scalability benefits introduced by the earlier PostgreSQL migration away from SQLite, while also creating a simpler click-to-update experience for staying current.  
- On the roadmap, but not yet implemented, is a bulk export path for user-created assemblies that would package all user-created assembly JSON files into a single ZIP, along with a matching bulk import path for bringing that ZIP into a new Engine installation.  

## Additional Changes
- Updated the UI of several engine webpages
- Redesign and refactor of device filter systems (Added Regex support and more robust logic)
