import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool
from langchain_groq import ChatGroq
# Import from the modern pydantic library directly.
from pydantic import BaseModel, Field

# --- THIS IS THE DEFINITIVE FIX ---
# Instead of wrapping the tool, we create our own custom tool
# that is smart enough to handle the LLM's quirky output.

from langchain.tools import tool

class CustomSerperDevTool:
    @tool("Search the internet with Serper")
    def search(search_query: dict) -> str:
        """
        A tool that can be used to search the internet with a search_query.
        It can handle complex dictionary inputs for the search query.
        """
        # The LLM is sending a dictionary like {'description': '...'},
        # so we extract the actual query from the 'description' key.
        query_to_use = search_query.get('description', '')
        if not isinstance(query_to_use, str):
             query_to_use = str(search_query) # Fallback if the structure is even weirder

        # Use the standard SerperDevTool to perform the actual search
        return SerperDevTool()._run(query=query_to_use)

# Instantiate our new, robust tool
search_tool = CustomSerperDevTool().search
# --- END OF FIX ---


# Get the research topic from the command-line argument
topic = sys.argv[1]

os.environ["GROQ_API_KEY"] = os.environ.get("GROQ_API_KEY")
os.environ["SERPER_API_KEY"] = os.environ.get("SERPER_API_KEY")

llm = ChatGroq(model_name="groq/llama-3.1-8b-instant")

# Define the Researcher Agent
researcher = Agent(
  role='Senior Research Analyst',
  goal=f'Uncover groundbreaking technologies and trends about {topic}',
  backstory="You're a renowned research analyst.",
  tools=[search_tool],
  llm=llm,
  max_rpm=2,
  verbose=True,
  allow_delegation=False
)

# Define the Writer Agent
writer = Agent(
  role='Senior Technology Writer',
  goal=f'Craft a compelling and informative blog post about {topic}',
  backstory="You're a famous technology writer.",
  llm=llm,
  max_rpm=2,
  verbose=True,
  allow_delegation=False
)

# Define the Tasks
research_task = Task(
  description=f'Conduct a comprehensive analysis of the latest trends in {topic}. Identify key players, innovations, and market forecasts.',
  expected_output='A detailed report summarizing your findings.',
  agent=researcher,
  max_retries=3
)

write_task = Task(
  description='Using the research report, write an engaging blog post. It should be easy to understand, well-structured, and highlight the most important findings.',
  expected_output=f'A 500-word blog post about {topic}, formatted in markdown.',
  agent=writer,
  max_retries=3
)

crew = Crew(
  agents=[researcher, writer],
  tasks=[research_task, write_task],
  process=Process.sequential,
  verbose=True
)

# Execute the crew's work
result = crew.kickoff()

# Print the final result as a JSON object to standard output
print(json.dumps({"result": result}))