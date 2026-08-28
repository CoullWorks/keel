import { z } from 'zod'

// A tracked project or monorepo member, as /api/projects returns it. Kept lax
// (passthrough) because the studio reads a stable subset; unknown fields ride
// along untouched.
export const memberSchema = z.object({
  name: z.string(),
  path: z.string(),
  framework: z.string().optional().default(''),
  env: z.string().optional().default(''),
  managed: z.boolean().optional().default(false),
}).passthrough()

export const projectSchema = z.object({
  name: z.string(),
  path: z.string(),
  framework: z.string().optional().default(''),
  env: z.string().optional().default(''),
  managed: z.boolean().optional().default(false),
  members: z.array(memberSchema).optional().default([]),
}).passthrough()

export type Project = z.infer<typeof projectSchema>
export type Member = z.infer<typeof memberSchema>

export const projectsResponse = z.object({
  projects: z.array(projectSchema).optional().default([]),
}).passthrough()

// /api/recipes — used at boot for the footer count + the build catalog.
export const recipesResponse = z.object({
  recipes: z.array(z.object({ id: z.string() }).passthrough()).optional().default([]),
  languages: z.array(z.string()).optional().default([]),
}).passthrough()

export const isMono = (p?: Project | Member | null) => !!p && (p as Project).framework === 'monorepo'
export const isLib = (p?: Project | Member | null) => !!p && p.framework === 'lib'
export const pManaged = (p?: Project | Member | null) => !!(p && p.managed)

// Backend is the shared shape of /api/.../backend — the effective DB/launch a
// project or monorepo member resolves to. It was re-declared in every project tab
// (ProjectDetail, Overview, Data, Secrets, Run, Generate, Manage, Deploy, Brand);
// hoisted here so the one shape is defined once. launchCommand is optional and
// only set on the run surface, so this superset satisfies every consumer.
export type Backend = {
  inherited?: boolean
  engine?: string
  provider?: string
  source?: string
  schema?: string
  rootLaunch?: boolean
  launchManager?: string
  launchCommand?: string
  error?: string
}
