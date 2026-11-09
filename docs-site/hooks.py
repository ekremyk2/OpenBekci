"""MkDocs hook: publish the bundled OpenAPI document next to the Redoc page."""
import os
import shutil


def on_post_build(config, **kwargs):
    src = os.path.join(os.path.dirname(config["config_file_path"]), "..", "api", "openapi.yaml")
    dst = os.path.join(config["site_dir"], "integrate", "openapi.yaml")
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copyfile(src, dst)
