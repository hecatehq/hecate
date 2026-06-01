package orchestrator

func attachTestQueueCoordinator(runner *Runner, queue RunQueue) {
	runner.queueCoordinator = newRunQueueCoordinator(runner, runQueueCoordinatorConfig{Queue: queue})
}
