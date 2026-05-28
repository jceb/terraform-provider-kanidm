from __future__ import annotations

from typing import TYPE_CHECKING

from kanidm_tf_import.hcl import HCLBuilder, q
from kanidm_tf_import.resolver import sanitize_tf_name

if TYPE_CHECKING:
    from kanidm_tf_import.client import KanidmClient

ACCOUNT_POLICY_ATTRS = [
    ("authsession_expiry", "int"),
    ("privilege_expiry", "int"),
    ("auth_password_minimum_length", "int"),
    ("credential_type_minimum", "str"),
    ("limit_search_max_results", "int"),
    ("limit_search_max_filter_test", "int"),
    ("allow_primary_cred_fallback", "bool"),
]


def read_account_policy_attrs(
    client: KanidmClient,
    group_id: str,
) -> dict[str, str | int | bool]:
    attrs: dict[str, str | int | bool] = {}
    for attr_name, attr_type in ACCOUNT_POLICY_ATTRS:
        values = client.get_group_attr(group_id, attr_name)
        if not values:
            continue
        raw = values[0]
        if attr_type == "int":
            try:
                attrs[attr_name] = int(raw)
            except (ValueError, TypeError):
                continue
        elif attr_type == "bool":
            attrs[attr_name] = raw.lower() == "true"
        else:
            attrs[attr_name] = raw
    return attrs


def generate_account_policy(
    group_name: str,
    group_id: str,
    client: KanidmClient,
    builder: HCLBuilder,
) -> str | None:
    attrs = read_account_policy_attrs(client, group_id)
    if not attrs:
        return None

    tf_name = sanitize_tf_name(group_name)
    resource_attrs: dict = {"group": q(group_name)}

    for attr_name, _ in ACCOUNT_POLICY_ATTRS:
        if attr_name in attrs:
            val = attrs[attr_name]
            if isinstance(val, bool):
                resource_attrs[attr_name] = val
            elif isinstance(val, int):
                resource_attrs[attr_name] = val
            else:
                resource_attrs[attr_name] = q(val)

    builder.resource("kanidm_account_policy", tf_name, **resource_attrs)
    return tf_name


def generate_system_denied_names(
    client: KanidmClient,
    builder: HCLBuilder,
) -> bool:
    names = client.get_system_attr("denied_name")
    if not names:
        return False

    builder.resource("kanidm_system_denied_names", "this", names=[q(n) for n in names])
    return True
