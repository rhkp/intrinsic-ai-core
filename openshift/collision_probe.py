# This project was developed with assistance from AI tools.
"""Read-only SDK collision evidence; emit fixed labels and numeric state only."""

from __future__ import annotations

import math
import re
import hashlib

MARKER = "INTRINSIC_COLLISION_TRACE="
STAGES = {
    "1. Infeed Pick Subtree": "pick",
    "2. Load Machine Subtree": "load",
    "3. Machining Handshake Subtree": "machining",
    "4. Unload Machine Subtree": "unload",
    "5. Return to Infeed Subtree": "return",
}
TASKS = {
    "Compliant Touchdown to Part (+Z Tool)": "pick_contact",
    "Compliant Seat Part into Vise (+Z Tool)": "load_contact",
    "Release Part in Vise": "load_release",
    "Detach raw_stock_2x3x5 from Gripper": "detach",
    "Retract Arm to Vise Approach (root/pre_place_vise)": "load_vertical_retreat",
    "Lift Clear of Released Part (-Z Tool)": "load_release_clearance",
    "Retract Arm to Machine Entry (root/machine_approach)": "load_exit",
    "Compliant Touchdown to Machined Part (+Z Tool)": "unload_contact",
    "Retract Machined Part from Machine (root/machine_approach)": "unload_exit",
    "Compliant Touchdown to Table Surface (+Z Tool)": "return_contact",
    "Release Finished Part": "return_release",
    "Retract Arm from Table (root/pre_grasp)": "return_vertical_retreat",
    "Return to View Pose (root/view)": "return_view",
    "Lift Clear of Returned Part (-Z Tool)": "return_release_clearance",
}
STATES = {
    "UNKNOWN",
    "UNSPECIFIED",
    "ACCEPTED",
    "RUNNING",
    "SUSPENDED",
    "SUCCEEDED",
    "FAILED",
    "CANCELED",
    "CANCELLED",
    "READY",
    "IDLE",
}
FRAMES = ("pre_grasp", "grasp", "pre_place_vise", "place_vise", "machine_approach")


def numbers(values) -> list[float]:
    result = [float(value) for value in values]
    if not all(math.isfinite(value) for value in result):
        raise ValueError("nonfinite_state")
    return result


def collision_facts(text: str) -> dict:
    """Never copy status prose, identifiers or endpoint strings to a report."""
    finger = re.search(r"gripper\.gripper_finger[12]\b", text)
    pair = bool(finger and "raw_stock_2x3x5.base_link" in text)
    segment = re.search(r"(?:planning (?:in |for )|motion )?segment (\d+)", text)
    joints = re.search(r"Configuration \[([^]\n]+)\] \(in rad\)", text)
    try:
        invalid = numbers(joints.group(1).split(",")) if joints else None
        if invalid is not None and len(invalid) != 6:
            invalid = None
    except ValueError:
        invalid = None
    return {
        "known_pair": pair,
        "category": "collision" if pair else "other" if text else "none",
        "planner_segment": int(segment.group(1)) if segment else None,
        "invalid_robot_joints_rad": invalid,
        "pair": [finger.group(), "raw_stock_2x3x5.base_link"] if pair else [],
    }


def messages(message):
    """Walk protobuf messages, including maps, without decoding arbitrary Any data."""
    yield message
    for field, value in message.ListFields():
        if field.type != field.TYPE_MESSAGE:
            continue
        children = (
            value.values()
            if field.is_repeated and hasattr(value, "values")
            else value
            if field.is_repeated
            else [value]
        )
        for child in children:
            if hasattr(child, "ListFields"):
                yield from messages(child)


def state(message, field_name="state") -> str:
    field = message.DESCRIPTOR.fields_by_name.get(field_name)
    if not field or not field.enum_type:
        return "UNKNOWN"
    enum = field.enum_type.values_by_number.get(getattr(message, field_name))
    name = enum.name.rsplit("_", 1)[-1] if enum else "UNKNOWN"
    return name if name in STATES else "UNKNOWN"


def pose(world, node) -> dict:
    transform = world.get_transform(world.root, node)
    return {
        "translation_m": numbers(transform.translation),
        "quaternion_xyzw": numbers(transform.rotation.quaternion.xyzw),
    }


def collect(solution) -> dict:
    """Compatible with a connected deployment SDK on either topology."""
    from google.protobuf import descriptor_pool, message_factory
    from intrinsic.manipulation.skills.force import move_to_contact_pb2  # noqa: F401

    operation = solution.executive.operation
    metadata = operation.metadata
    stage_states, task_states, contacts = {}, {}, {}
    nodes = list(messages(metadata.behavior_tree))
    for node in nodes:
        name = getattr(node, "name", "")
        if name in STAGES:
            stage_states[STAGES[name]] = state(node)
        if name in TASKS:
            # The detach task occurs in several stages; its state is not a cycle gate.
            task_states[TASKS[name]] = state(node)
        if name not in TASKS or not hasattr(node, "task"):
            continue
        call = node.task.call_behavior
        if call.skill_id != "ai.intrinsic.move_to_contact":
            continue
        descriptor = descriptor_pool.Default().FindMessageTypeByName(
            call.parameters.TypeName()
        )
        parameters = message_factory.GetMessageClass(descriptor)()
        if not call.parameters.Unpack(parameters):
            raise ValueError("contact_parameters_unavailable")
        direction = parameters.fixed_vector.direction
        contacts[TASKS[name]] = {
            "direction_tool": numbers([direction.x, direction.y, direction.z]),
            "force_newtons": float(parameters.contact_force),
            "timeout_seconds": float(parameters.timeout_sec),
        }
    world = solution.world
    gripper = world.get_object("gripper")
    workpiece = world.get_object("raw_stock_2x3x5")
    robot = world.get_object("ur_module")
    geometry = {}
    for name, obj in (("gripper", gripper), ("workpiece", workpiece)):
        geometry[name] = sorted(
            hashlib.sha256(
                entity.geometry_component.named_geometries[
                    "Intrinsic_Collision"
                ].SerializeToString(deterministic=True)
            ).hexdigest()
            for entity in obj.proto.entities.values()
            if "Intrinsic_Collision" in entity.geometry_component.named_geometries
        )
    complete = (
        bool(operation.done)
        and state(metadata, "operation_state") == "SUCCEEDED"
        and set(stage_states) == set(STAGES.values())
        and all(value == "SUCCEEDED" for value in stage_states.values())
    )
    return {
        "schema_version": 1,
        "operation_done": bool(operation.done),
        "cycle_completed": complete,
        "operation_state": state(metadata, "operation_state"),
        "stages": stage_states,
        "tasks": task_states,
        "failure": collision_facts(
            str(operation.extended_status) if operation.extended_status else ""
        ),
        "contact_commands": contacts,
        "collision_geometry_sha256": geometry,
        "belief_world": {
            "robot_joints_rad": numbers(robot.joint_positions),
            "gripper_joints_m": numbers(gripper.joint_positions),
            "tool": pose(world, gripper.get_frame("tool_frame")),
            "workpiece": pose(world, workpiece),
            "workpiece_attached_to_gripper": workpiece.parent_name == "gripper",
            "frames": {
                name: pose(world, world.get_frame(name, object_name="root"))
                for name in FRAMES
            },
        },
        "simulator_joint_feedback_observed": False,
        "contact_points_observed": False,
    }
