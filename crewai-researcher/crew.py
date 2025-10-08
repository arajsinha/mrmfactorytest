import sys
import json
import os
import requests
import uuid
from crewai import Agent, Task, Crew, Process
from langchain_cohere import ChatCohere
from typing import Type, Any

# --- THIS IS THE FIX ---
# We must import the BaseTool class that our custom tool is based on.
from crewai.tools.base_tool import BaseTool
from pydantic import BaseModel, Field
# --- END OF FIX ---

# --- The Custom Tool for State Management ---
class StorageToolInput(BaseModel):
    action: str = Field(description="The action to perform: 'save' or 'get'.")
    task_id: str = Field(description="The unique ID of the task to save or retrieve.")
    data: Any = Field(description="The content to save. Only used with the 'save' action.", default=None)

class EtcdStorageTool(BaseTool):
    name: str = "Persistent Task Storage"
    description: str = "A tool to save the output of a completed task or get the output of a previous task."
    args_schema: Type[BaseModel] = StorageToolInput
    
    def _run(self, action: str, task_id: str, data: Any = None) -> str:
        # The internal Kubernetes DNS name for our MCP server
        mcp_server_url = "http://mcp-server.default.svc.cluster.local"
        
        if action == "save":
            try:
                payload = {"id": task_id, "status": "completed", "output": data}
                response = requests.post(f"{mcp_server_url}/tasks/{task_id}", json=payload)
                response.raise_for_status()
                return f"Successfully saved output for task {task_id}"
            except Exception as e:
                return f"Error saving task output: {e}"
        elif action == "get":
            try:
                response = requests.get(f"{mcp_server_url}/tasks/{task_id}")
                response.raise_for_status()
                task_data = response.json()
                return task_data.get("output", "No output found for this task.")
            except Exception as e:
                return f"Error getting task output: {e}"
        else:
            return "Invalid action. Use 'save' or 'get'."

# --- Initialization ---
storage_tool = EtcdStorageTool()
topic = sys.argv[1]

os.environ["COHERE_API_KEY"] = os.environ.get("COHERE_API_KEY")

llm = ChatCohere(
    model="command-r-plus",
    cohere_api_key=os.environ.get("COHERE_API_KEY")
)

# --- Agents ---
writer = Agent(
    role="Creative Content Writer",
    goal=f"Write a short, engaging, and informative blog post about '{topic}'.",
    backstory="You're a skilled content writer...",
    tools=[storage_tool],
    llm=llm,
    verbose=True,
)

editor = Agent(
    role="Senior Editor",
    goal="Polish the writing for clarity, grammar, and tone.",
    backstory="You're an experienced editor...",
    tools=[storage_tool],
    llm=llm,
    verbose=True,
)

# --- Tasks ---
writer_task_id = f"writer-draft-{uuid.uuid4()}"
editor_task_id = f"editor-final-{uuid.uuid4()}"

write_task = Task(
  description=f'Write a 250-word blog post about "{topic}". After writing, you MUST use the Persistent Task Storage tool to save your final draft. Use the action "save" and the task_id "{writer_task_id}".',
  expected_output='Confirmation that the draft was saved successfully.',
  agent=writer
)

edit_task = Task(
  description=f'First, use the Persistent Task Storage tool to retrieve the blog post draft. Use the action "get" and the task_id "{writer_task_id}". Then, review the draft and output the final, polished version. After editing, you MUST use the Persistent Task Storage tool to save your final version with the task_id "{editor_task_id}".',
  expected_output='The final, polished version of the blog post in markdown format.',
  agent=editor
)

# --- Crew ---
crew = Crew(
  agents=[writer, editor],
  tasks=[write_task, edit_task],
  process=Process.sequential,
  verbose=True
)

# --- Execution ---
result = crew.kickoff()

# --- Safe Serialization ---
def safe_serialize(obj):
    if hasattr(obj, "model_dump"): return obj.model_dump()
    if hasattr(obj, "__dict__"): return obj.__dict__
    return str(obj)

final_output = {
    "final_result": result.raw if result else "No result",
    "token_usage": safe_serialize(result.token_usage) if result and hasattr(result, 'token_usage') else {}
}

print(json.dumps(final_output, indent=2, default=safe_serialize))