# OpenConveyor

OpenConveyor runs AI coding agents as one-shot, hardened Kubernetes Jobs. One `Task` resource describes one run. Cluster-scoped classes describe the agent image and the trigger that created the Task.

## Language

**Task**:
One requested run of an agent: a prompt, the agent that runs it, the permissions that it can use, and a mandatory timeout. A Task has a namespace. Every resource that the controller creates for the run belongs to the Task.
_Avoid_: job, run, TaskRun

**Terminal Task**:
A Task with the phase Completed, Failed or TimedOut. The controller never runs it again.
_Avoid_: finished Task, done Task, dead Task

**Guarantee**:
One of the four security defaults that every Task has without an opt-in. The four are default-deny egress, no RBAC, secrets as files, and a mandatory hard timeout. A guarantee is never a setting.
_Avoid_: feature, default setting, option

**Agent**:
A container image that reads a prompt and does the work. It knows nothing about OpenConveyor except the file contract that OpenConveyor starts it with.
_Avoid_: bot, worker, runner

**ClusterAgentClass**:
A cluster-scoped description of an Agent: its image and the inputs that it accepts. It also gives the baseline permissions that every Task with this class has.
_Avoid_: agent template, agent profile

**Reference agent**:
One of the three agent images that this repo ships: the implementer, the reviewer and the reporter. Each one is a worked example of the agent contract, not the only agent that can run.
_Avoid_: built-in agent, official agent

**Trigger**:
Anything that creates a Task. Triggers are outside the controller and call only the Kubernetes API.
_Avoid_: source, integration, hook

**ClusterTriggerClass**:
A cluster-scoped description of a webhook Trigger: how the trigger adapter verifies, filters and maps an incoming event into a Task.
_Avoid_: webhook config, trigger template
