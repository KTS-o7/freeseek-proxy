import base64
import json
import os
from typing import Any, Dict

import numpy as np
import wasmtime

WASM_PATH = f"{os.path.dirname(__file__)}/wasm/sha3_wasm_bg.7b9ca65ddd.wasm"
DEBUG_POW = os.getenv("DEEPSEEK_POW_DEBUG", "").strip().lower() in {
    "1",
    "true",
    "yes",
    "on",
}


def _debug(message: str) -> None:
    if DEBUG_POW:
        print(f"[pow] {message}", flush=True)


class DeepSeekHash:
    def __init__(self):
        self.instance = None
        self.memory = None
        self.store = None

    def init(self, wasm_path: str):
        _debug(f"init start wasm_path={wasm_path}")
        engine = wasmtime.Engine()

        with open(wasm_path, "rb") as f:
            wasm_bytes = f.read()
        _debug(f"read wasm bytes size={len(wasm_bytes)}")

        module = wasmtime.Module(engine, wasm_bytes)
        _debug("compiled wasm module")

        self.store = wasmtime.Store(engine)
        linker = wasmtime.Linker(engine)
        linker.define_wasi()
        _debug("created store and linker")

        self.instance = linker.instantiate(self.store, module)
        _debug("instantiated wasm module")
        self.memory = self.instance.exports(self.store)["memory"]
        _debug("resolved exported memory")

        return self

    def _write_to_memory(self, text: str) -> tuple[int, int]:
        encoded = text.encode("utf-8")
        length = len(encoded)
        preview = text[:80].replace("\n", "\\n")
        _debug(f"_write_to_memory start length={length} preview={preview!r}")
        ptr = self.instance.exports(self.store)["__wbindgen_export_0"](
            self.store, length, 1
        )
        _debug(f"_write_to_memory allocated ptr={ptr} length={length}")

        memory_view = self.memory.data_ptr(self.store)
        _debug("_write_to_memory acquired memory view")
        for i, byte in enumerate(encoded):
            memory_view[ptr + i] = byte

        _debug(f"_write_to_memory finished ptr={ptr} length={length}")
        return ptr, length

    def calculate_hash(
        self, algorithm: str, challenge: str, salt: str, difficulty: int, expire_at: int
    ) -> float:
        _debug(
            "calculate_hash start "
            f"algorithm={algorithm} challenge_len={len(challenge)} salt_len={len(salt)} "
            f"difficulty={difficulty} expire_at={expire_at}"
        )

        prefix = f"{salt}_{expire_at}_"
        _debug(f"calculate_hash prefix_len={len(prefix)}")
        retptr = self.instance.exports(self.store)["__wbindgen_add_to_stack_pointer"](
            self.store, -16
        )
        _debug(f"calculate_hash retptr={retptr}")

        try:
            _debug("calculate_hash before challenge write")
            challenge_ptr, challenge_len = self._write_to_memory(challenge)
            _debug(
                f"calculate_hash after challenge write ptr={challenge_ptr} len={challenge_len}"
            )
            _debug("calculate_hash before prefix write")
            prefix_ptr, prefix_len = self._write_to_memory(prefix)
            _debug(
                f"calculate_hash after prefix write ptr={prefix_ptr} len={prefix_len}"
            )

            _debug("calculate_hash before wasm_solve")
            self.instance.exports(self.store)["wasm_solve"](
                self.store,
                retptr,
                challenge_ptr,
                challenge_len,
                prefix_ptr,
                prefix_len,
                float(difficulty),
            )
            _debug("calculate_hash after wasm_solve")

            memory_view = self.memory.data_ptr(self.store)
            _debug("calculate_hash acquired output memory view")
            status_bytes = bytes(memory_view[retptr : retptr + 4])
            _debug(f"calculate_hash raw status bytes={status_bytes!r}")
            status = int.from_bytes(status_bytes, byteorder="little", signed=True)
            _debug(f"calculate_hash status={status}")

            if status == 0:
                _debug("calculate_hash status=0 returning None")
                return None

            value_bytes = bytes(memory_view[retptr + 8 : retptr + 16])
            _debug(f"calculate_hash raw value bytes={value_bytes!r}")
            value = np.frombuffer(value_bytes, dtype=np.float64)[0]
            _debug(f"calculate_hash decoded value={value}")

            result = int(value)
            _debug(f"calculate_hash returning result={result}")
            return result

        finally:
            _debug("calculate_hash restoring stack pointer")
            self.instance.exports(self.store)["__wbindgen_add_to_stack_pointer"](
                self.store, 16
            )
            _debug("calculate_hash finished")


class DeepSeekPOW:
    def __init__(self):
        _debug("DeepSeekPOW init start")
        self.hasher = DeepSeekHash().init(WASM_PATH)
        _debug("DeepSeekPOW init complete")

    def solve_challenge(self, config: Dict[str, Any]) -> str:
        """Solves a proof-of-work challenge and returns the encoded response"""
        _debug(
            "solve_challenge start "
            f"algorithm={config.get('algorithm')} "
            f"challenge_len={len(str(config.get('challenge', '')))} "
            f"salt_len={len(str(config.get('salt', '')))} "
            f"difficulty={config.get('difficulty')} "
            f"expire_at={config.get('expire_at')}"
        )
        answer = self.hasher.calculate_hash(
            config["algorithm"],
            config["challenge"],
            config["salt"],
            config["difficulty"],
            config["expire_at"],
        )
        _debug(f"solve_challenge answer={answer}")

        result = {
            "algorithm": config["algorithm"],
            "challenge": config["challenge"],
            "salt": config["salt"],
            "answer": answer,
            "signature": config["signature"],
            "target_path": config["target_path"],
        }

        encoded = base64.b64encode(json.dumps(result).encode()).decode()
        _debug(f"solve_challenge encoded_length={len(encoded)}")
        return encoded
