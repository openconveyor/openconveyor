# OpenConveyor

OpenConveyor runs AI coding agents as one-shot, hardened Kubernetes Jobs. A single `Task` resource describes one run; cluster-scoped classes describe the agent image and the trigger that produced it.

## Language

**Task**:
One requested run of an agent: a prompt, the agent to run it, the permissions it may use, and a mandatory timeout. Namespaced; everything created for the run belongs to it.
_Avoid_: job, run, TaskRun

**Terminal Task**:
A Task whose phase is Completed, Failed or TimedOut. It will never run again.
_Avoid_: finished Task, done Task, dead Task

**Agent**:
A container image that reads a prompt and does the work. It knows nothing about OpenConveyor beyond the file contract it is started with.
_Avoid_: bot, worker, runner

**ClusterAgentClass**:
A cluster-scoped description of an Agent: its image, the inputs it accepts, and the baseline permissions every Task using it gets.
_Avoid_: agent template, agent profile

**Trigger**:
Anything that creates a Task. Triggers live outside the controller and only touch the Kubernetes API.
_Avoid_: source, integration, hook

**ClusterTriggerClass**:
A cluster-scoped description of a webhook Trigger: how an incoming event is verified, filtered and mapped into a Task.
_Avoid_: webhook config, trigger template
