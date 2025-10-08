import sys
import json
import os
import requests
import uuid
from crewai import Agent, Task, Crew, Process
from langchain_cohere import ChatCohere
from crewai.tools.base_tool import BaseTool
from pydantic import BaseModel, Field
from typing import Type, Any

# --- Custom Storage Tool (ETCD) ---
class StorageToolInput(BaseModel):
    action: str = Field(description="The action to perform: 'save' or 'get'.")
    task_id: str = Field(description="The unique ID of the task to save or retrieve.")
    data: Any = Field(description="The content to save. Only used with the 'save' action.", default=None)

class EtcdStorageTool(BaseTool):
    name: str = "Persistent Task Storage"
    description: str = "Save or retrieve task data via MCP server connected to etcd."
    args_schema: Type[BaseModel] = StorageToolInput
    
    def _run(self, action: str, task_id: str, data: Any = None) -> str:
        mcp_server_url = "http://mcp-server.default.svc.cluster.local"
        if action == "save":
            try:
                print(f"\n--- 📝 TOOL LOG: Saving data for task '{task_id}' ---\n")
                payload = {"id": task_id, "status": "completed", "output": data}
                response = requests.post(f"{mcp_server_url}/tasks/{task_id}", json=payload)
                response.raise_for_status()
                return f"Successfully saved output for task {task_id}"
            except Exception as e:
                return f"Error saving task output: {e}"
        elif action == "get":
            try:
                print(f"\n--- 🗂️ TOOL LOG: Getting data for task '{task_id}' ---\n")
                response = requests.get(f"{mcp_server_url}/tasks/{task_id}")
                response.raise_for_status()
                task_data = response.json()
                return task_data.get("output", "No output found for this task.")
            except Exception as e:
                return f"Error getting task output: {e}"
        else:
            return "Invalid action. Use 'save' or 'get'."

# --- Serper Tool for Research ---
class SerperToolInput(BaseModel):
    query: str = Field(description="Search query to find information about the topic.")

class SerperTool(BaseTool):
    name: str = "Serper Research Tool"
    description: str = "Use Google Search via Serper API to gather recent and relevant information."
    args_schema: Type[BaseModel] = SerperToolInput

    def _run(self, query: str) -> str:
        serper_api_key = os.environ.get("SERPER_API_KEY")
        if not serper_api_key:
            return "Missing SERPER_API_KEY in environment."
        try:
            print(f"\n--- 🌐 TOOL LOG: Running Serper search for '{query}' ---\n")
            response = requests.post(
                "https://google.serper.dev/search",
                headers={"X-API-KEY": serper_api_key, "Content-Type": "application/json"},
                json={"q": query, "num": 5}
            )
            response.raise_for_status()
            data = response.json()
            results = []
            for item in data.get("organic", []):
                results.append(f"{item.get('title')}: {item.get('snippet')}")
            return "\n\n".join(results[:5]) or "No results found."
        except Exception as e:
            return f"Error during Serper search: {e}"

# --- Initialization ---
storage_tool = EtcdStorageTool()
serper_tool = SerperTool()
topic = sys.argv[1]

llm = ChatCohere(
    model="command-r-plus",
    cohere_api_key=os.environ.get("COHERE_API_KEY")
)

# --- Agents ---
researcher = Agent(
    role="Research Analyst",
    goal=f"Find the most recent and relevant information about '{topic}' using the web.",
    backstory="You specialize in quick, factual research to help writers with context and data.",
    tools=[serper_tool, storage_tool],
    llm=llm,
    verbose=True,
)

writer = Agent(
    role="Creative Content Writer",
    goal=f"Write a short, engaging, and informative blog post about '{topic}' using research data.",
    backstory="You're a skilled content writer who transforms research into compelling storytelling.",
    tools=[storage_tool],
    llm=llm,
    verbose=True,
)

editor = Agent(
    role="Senior Editor",
    goal="Polish the writing for clarity, grammar, and tone.",
    backstory="You're an experienced editor ensuring consistency and professionalism.",
    tools=[storage_tool],
    llm=llm,
    verbose=True,
)

# --- Task IDs ---
research_task_id = f"research-{uuid.uuid4()}"
writer_task_id = f"writer-draft-{uuid.uuid4()}"
editor_task_id = f"editor-final-{uuid.uuid4()}"

# --- Tasks ---
research_task = Task(
  description=f'Use the Serper Research Tool to find up-to-date information about "{topic}". Summarize your findings into a research brief (150-200 words). Then use the Persistent Task Storage tool to save the results with action "save" and task_id "{research_task_id}".',
  expected_output="Confirmation that the research summary was saved successfully.",
  agent=researcher
)

write_task = Task(
  description=f'First, use the Persistent Task Storage tool to retrieve the research brief using action "get" and task_id "{research_task_id}". Based on that, write a 250-word engaging blog post about "{topic}". Then use the Persistent Task Storage tool to save your draft with task_id "{writer_task_id}".',
  expected_output='Confirmation that the blog draft was saved successfully.',
  agent=writer
)

edit_task = Task(
  description=f'First, use the Persistent Task Storage tool to retrieve the blog draft using action "get" and task_id "{writer_task_id}". Then polish it and save the final markdown version using action "save" with task_id "{editor_task_id}".',
  expected_output='Final, polished blog post in markdown format.',
  agent=editor
)

# --- Crew ---
crew = Crew(
  agents=[researcher, writer, editor],
  tasks=[research_task, write_task, edit_task],
  process=Process.sequential,
  verbose=True
)

# --- Execute ---
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
