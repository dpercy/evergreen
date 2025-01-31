package model

import (
	"github.com/mongodb/grip"
)

type dependencyIncluder struct {
	Project  *Project
	included map[TVPair]bool
}

// Include crawls the tasks represented by the combination of variants and tasks and
// add or removes tasks based on the dependency graph. Required and dependent tasks
// are added; tasks that depend on unpatchable tasks are pruned. New slices
// of variants and tasks are returned.
func (di *dependencyIncluder) Include(initialDeps []TVPair) []TVPair {
	di.included = map[TVPair]bool{}

	// handle each pairing, recursively adding and pruning based
	// on the task's requirements and dependencies
	for _, d := range initialDeps {
		di.handle(d)
	}

	outPairs := []TVPair{}
	for pair, shouldInclude := range di.included {
		if shouldInclude {
			outPairs = append(outPairs, pair)
		}
	}
	return outPairs
}

// handle finds and includes all tasks that the given task/variant pair
// requires or depends on. Returns true if the task and all of its
// dependent/required tasks are patchable, false if they are not.
func (di *dependencyIncluder) handle(pair TVPair) bool {
	if included, ok := di.included[pair]; ok {
		// we've been here before, so don't redo work
		return included
	}

	// if the given task is a task group, recurse on each task
	if tg := di.Project.FindTaskGroup(pair.TaskName); tg != nil {
		for _, t := range tg.Tasks {
			if ok := di.handle(TVPair{TaskName: t, Variant: pair.Variant}); !ok {
				di.included[pair] = false
				return false // task depends on an unpatchable task, so skip it
			}
		}
		return true
	}

	// we must load the BuildVariantTaskUnit for the task/variant pair,
	// since it contains the full scope of dependency information
	bvt := di.Project.FindTaskForVariant(pair.TaskName, pair.Variant)
	if bvt == nil {
		grip.Errorf("task %s does not exist in project %s", pair.TaskName,
			di.Project.Identifier)
		di.included[pair] = false
		return false // task not found in project--skip it.
	}

	if patchable := bvt.Patchable; patchable != nil && !*patchable {
		di.included[pair] = false
		return false // task cannot be patched, so skip it
	}
	di.included[pair] = true

	// queue up all requirements and dependencies for recursive inclusion
	deps := append(
		di.expandRequirements(pair, bvt.Requires),
		di.expandDependencies(pair, bvt.DependsOn)...)
	for _, dep := range deps {
		if ok := di.handle(dep); !ok {
			di.included[pair] = false
			return false // task depends on an unpatchable task, so skip it
		}
	}

	// we've reached a point where we know it is safe to include the current task
	return true
}

// expandRequirements finds all tasks required by the current task/variant pair.
func (di *dependencyIncluder) expandRequirements(pair TVPair, reqs []TaskUnitRequirement) []TVPair {
	deps := []TVPair{}
	for _, r := range reqs {
		if r.Variant == AllVariants {
			// the case where we depend on all variants for a task
			for _, v := range di.Project.FindVariantsWithTask(r.Name) {
				if v != pair.Variant { // skip current variant
					deps = append(deps, TVPair{TaskName: r.Name, Variant: v})
				}
			}
		} else {
			// otherwise we're depending on a single task for a single variant
			// We simply add a single task/variant and its dependencies.
			v := r.Variant
			if v == "" {
				v = pair.Variant
			}
			deps = append(deps, TVPair{TaskName: r.Name, Variant: v})
		}
	}
	return deps
}

// expandRequirements finds all tasks depended on by the current task/variant pair.
func (di *dependencyIncluder) expandDependencies(pair TVPair, depends []TaskUnitDependency) []TVPair {
	deps := []TVPair{}
	for _, d := range depends {
		// don't automatically add dependencies if they are marked patch_optional
		if d.PatchOptional {
			continue
		}
		switch {
		case d.Variant == AllVariants && d.Name == AllDependencies: // task = *, variant = *
			// Here we get all variants and tasks (excluding the current task)
			// and add them to the list of tasks and variants.
			for _, v := range di.Project.FindAllVariants() {
				for _, t := range di.Project.FindTasksForVariant(v) {
					if !(t == pair.TaskName && v == pair.Variant) {
						deps = append(deps, TVPair{TaskName: t, Variant: v})
					}
				}
			}

		case d.Variant == AllVariants: // specific task, variant = *
			// In the case where we depend on a task on all variants, we fetch the task's
			// dependencies, then add that task for all variants that have it.
			for _, v := range di.Project.FindVariantsWithTask(d.Name) {
				if !(pair.TaskName == d.Name && pair.Variant == v) {
					deps = append(deps, TVPair{TaskName: d.Name, Variant: v})
				}
			}

		case d.Name == AllDependencies: // task = *, specific variant
			// Here we add every task for a single variant. We add the dependent variant,
			// then add all of that variant's task, as well as their dependencies.
			v := d.Variant
			if v == "" {
				v = pair.Variant
			}
			for _, t := range di.Project.FindTasksForVariant(v) {
				if !(pair.TaskName == t && pair.Variant == v) {
					deps = append(deps, TVPair{TaskName: t, Variant: v})
				}
			}

		default: // specific name, specific variant
			// We simply add a single task/variant and its dependencies.
			v := d.Variant
			if v == "" {
				v = pair.Variant
			}
			deps = append(deps, TVPair{TaskName: d.Name, Variant: v})
		}
	}
	return deps
}
