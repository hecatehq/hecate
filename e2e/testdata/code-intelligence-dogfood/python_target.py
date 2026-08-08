def dogfood_python_target(value: str) -> str:
    return "CODEINTEL_PYTHON_READY:" + value


def dogfood_python_decoy(value: str) -> str:
    return "CODEINTEL_PYTHON_DECOY:" + value
